package fastcgi

import (
	"fmt"
	"net"
	"strings"

	"github.com/uwaserver/uwas/internal/router"
)

// BuildEnv constructs the CGI/FastCGI environment variables for a request.
func BuildEnv(ctx *router.RequestContext, scriptFilename, scriptName, pathInfo string, customEnv map[string]string) map[string]string {
	r := ctx.Request

	env := map[string]string{
		"GATEWAY_INTERFACE": "CGI/1.1",
		"SERVER_PROTOCOL":   r.Proto,
		"SERVER_SOFTWARE":   "UWAS",
		"SERVER_NAME":       r.Host,
		"REQUEST_METHOD":    r.Method,
		"REQUEST_URI":       ctx.OriginalURI,
		"QUERY_STRING":      r.URL.RawQuery,
		"DOCUMENT_ROOT":     ctx.DocumentRoot,
		"SCRIPT_FILENAME":   scriptFilename,
		"SCRIPT_NAME":       scriptName,
		"PATH_INFO":         pathInfo,
		"REMOTE_ADDR":       clientIP(r.RemoteAddr),
		"REMOTE_PORT":       clientPort(r.RemoteAddr),
		"CONTENT_TYPE":      r.Header.Get("Content-Type"),
		"CONTENT_LENGTH":    r.Header.Get("Content-Length"),
	}

	// HTTP_HOST — Go stores Host header in r.Host, not r.Header
	if r.Host != "" {
		env["HTTP_HOST"] = r.Host
	}

	// Server port
	if ctx.IsHTTPS {
		env["HTTPS"] = "on"
		env["SERVER_PORT"] = "443"
	} else {
		env["SERVER_PORT"] = "80"
	}

	// Real IP override
	if ctx.RemoteIP != "" {
		env["REMOTE_ADDR"] = ctx.RemoteIP
	}

	// Forward HTTP headers as HTTP_* variables
	for key, vals := range r.Header {
		// Skip hop-by-hop and already-set headers
		upper := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		if upper == "CONTENT_TYPE" || upper == "CONTENT_LENGTH" {
			continue
		}
		env["HTTP_"+upper] = strings.Join(vals, ", ")
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

// SplitScriptPath splits a URI into script name and path info.
// For "/index.php/controller/action" → ("/index.php", "/controller/action")
func SplitScriptPath(uri, docRoot string, indexFiles []string) (scriptName, pathInfo string) {
	// Direct .php file
	if strings.HasSuffix(uri, ".php") {
		return uri, ""
	}

	// Check for .php in path segments: /index.php/extra/path
	parts := strings.Split(uri, "/")
	for i, part := range parts {
		if strings.HasSuffix(part, ".php") {
			scriptName = strings.Join(parts[:i+1], "/")
			if i+1 < len(parts) {
				pathInfo = "/" + strings.Join(parts[i+1:], "/")
			}
			return scriptName, pathInfo
		}
	}

	// Fallback to first index file
	if len(indexFiles) > 0 {
		scriptName = "/" + indexFiles[0]
		if uri != "/" {
			pathInfo = uri
		}
		return scriptName, pathInfo
	}

	return uri, ""
}

// ScriptFilename constructs the full filesystem path for the PHP script.
func ScriptFilename(docRoot, scriptName string) string {
	return fmt.Sprintf("%s%s", strings.TrimRight(docRoot, "/"), scriptName)
}
