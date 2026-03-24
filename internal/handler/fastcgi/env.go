package fastcgi

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/router"
)

// BuildEnv constructs the CGI/FastCGI environment variables for a request.
func BuildEnv(ctx *router.RequestContext, scriptFilename, scriptName, pathInfo string, customEnv map[string]string) map[string]string {
	r := ctx.Request

	env := map[string]string{
		"GATEWAY_INTERFACE": "CGI/1.1",
		"SERVER_PROTOCOL":   r.Proto,
		"SERVER_SOFTWARE":   "Apache/2.4 (UWAS)",
		"SERVER_NAME":       r.Host,
		"REQUEST_METHOD":    r.Method,
		"REQUEST_URI":       ctx.OriginalURI,
		"DOCUMENT_URI":      scriptName,
		"QUERY_STRING":      r.URL.RawQuery,
		"DOCUMENT_ROOT":     ctx.DocumentRoot,
		"SCRIPT_FILENAME":   scriptFilename,
		"SCRIPT_NAME":       scriptName,
		"PATH_INFO":         pathInfo,
		"REDIRECT_STATUS":   "200",
		"REMOTE_ADDR":       clientIP(r.RemoteAddr),
		"REMOTE_PORT":       clientPort(r.RemoteAddr),
		"CONTENT_TYPE":      r.Header.Get("Content-Type"),
		"CONTENT_LENGTH":    r.Header.Get("Content-Length"),
	}

	// HTTP_HOST — Go stores Host header in r.Host, not r.Header
	if r.Host != "" {
		env["HTTP_HOST"] = r.Host
	}

	// Server port — extract from actual Host header or listener
	serverPort := "80"
	if ctx.IsHTTPS {
		env["HTTPS"] = "on"
		serverPort = "443"
	}
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" {
		serverPort = port
	}
	env["SERVER_PORT"] = serverPort

	// Real IP override (from trusted proxy headers)
	if ctx.RemoteIP != "" {
		env["REMOTE_ADDR"] = ctx.RemoteIP
	}

	// Forward HTTP headers as HTTP_* variables
	for key, vals := range r.Header {
		upper := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		if upper == "CONTENT_TYPE" || upper == "CONTENT_LENGTH" {
			continue
		}
		env["HTTP_"+upper] = strings.Join(vals, ", ")
	}

	// PATH_TRANSLATED: document root + path info
	if pathInfo != "" {
		env["PATH_TRANSLATED"] = strings.TrimRight(ctx.DocumentRoot, "/") + pathInfo
	}

	// Custom per-domain environment variables
	for k, v := range customEnv {
		env[k] = v
	}

	// Remove empty values
	for k, v := range env {
		if v == "" {
			delete(env, k)
		}
	}

	return env
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func clientPort(remoteAddr string) string {
	_, port, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return ""
	}
	return port
}

// SplitScriptPath determines the SCRIPT_NAME and PATH_INFO for a PHP request.
//
// It takes both the original URI (before rewrites) and the resolved file path
// (after try_files/rewrites). This matches Apache/Nginx behavior:
//   - SCRIPT_NAME = the .php file relative to docRoot
//   - PATH_INFO = the original URI path (for front-controller apps)
//
// Examples:
//   - Request /index.php → SCRIPT_NAME=/index.php, PATH_INFO=""
//   - Request /index.php/api/users → SCRIPT_NAME=/index.php, PATH_INFO=/api/users
//   - Request /blog/my-post (resolved to /index.php) → SCRIPT_NAME=/index.php, PATH_INFO=/blog/my-post
//   - Request /wp-admin/admin.php → SCRIPT_NAME=/wp-admin/admin.php, PATH_INFO=""
func SplitScriptPath(originalURI, resolvedPath, docRoot string, indexFiles []string) (scriptName, pathInfo string) {
	// Strip query string from original URI
	origPath := originalURI
	if qIdx := strings.Index(origPath, "?"); qIdx != -1 {
		origPath = origPath[:qIdx]
	}

	// If we have a resolved filesystem path, derive SCRIPT_NAME from it
	if resolvedPath != "" {
		absRoot, _ := filepath.Abs(docRoot)
		absResolved, _ := filepath.Abs(resolvedPath)
		if strings.HasPrefix(absResolved, absRoot) {
			rel := absResolved[len(absRoot):]
			scriptName = filepath.ToSlash(rel)
			if !strings.HasPrefix(scriptName, "/") {
				scriptName = "/" + scriptName
			}
		}
	}

	// If resolved path contains .php in the middle of the original URI,
	// split there: /index.php/api/users → scriptName=/index.php, pathInfo=/api/users
	if scriptName == "" {
		parts := strings.Split(origPath, "/")
		for i, part := range parts {
			if strings.HasSuffix(part, ".php") {
				scriptName = strings.Join(parts[:i+1], "/")
				if i+1 < len(parts) {
					pathInfo = "/" + strings.Join(parts[i+1:], "/")
				}
				return scriptName, pathInfo
			}
		}
	}

	// If resolved to a .php file and original URI is different → front-controller
	if strings.HasSuffix(scriptName, ".php") && scriptName != origPath && origPath != "/" {
		// Original URI didn't point to a .php file; this is a rewrite/try_files fallback
		// PHP apps expect PATH_INFO = original URI
		pathInfo = origPath
		return scriptName, pathInfo
	}

	// Direct .php request (no rewrite): /wp-admin/admin.php
	if strings.HasSuffix(scriptName, ".php") {
		return scriptName, ""
	}

	// Fallback: use first index file
	if len(indexFiles) > 0 {
		scriptName = "/" + indexFiles[0]
		if origPath != "/" {
			pathInfo = origPath
		}
		return scriptName, pathInfo
	}

	return origPath, ""
}

// ScriptFilename constructs the full filesystem path for the PHP script.
func ScriptFilename(docRoot, scriptName string) string {
	// If resolvedPath is available and exists, use it directly
	return fmt.Sprintf("%s%s", strings.TrimRight(docRoot, "/"), scriptName)
}

// ScriptFilenameFromResolved returns the resolved path if it exists,
// otherwise constructs from docRoot + scriptName.
func ScriptFilenameFromResolved(resolvedPath, docRoot, scriptName string) string {
	if resolvedPath != "" {
		if _, err := os.Stat(resolvedPath); err == nil {
			return resolvedPath
		}
	}
	return fmt.Sprintf("%s%s", strings.TrimRight(docRoot, "/"), scriptName)
}
