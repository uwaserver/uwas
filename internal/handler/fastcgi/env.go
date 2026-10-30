package fastcgi

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/router"
)

// BuildEnv constructs the CGI/FastCGI environment variables for a request.
func BuildEnv(ctx *router.RequestContext, scriptFilename, scriptName, pathInfo string, customEnv map[string]string, maxUpload config.ByteSize) map[string]string {
	r := ctx.Request

	env := map[string]string{
		"GATEWAY_INTERFACE": "CGI/1.1",
		"SERVER_PROTOCOL":   r.Proto,
		"SERVER_SOFTWARE":   "Apache/2.4 (UWAS)",
		"SERVER_NAME":       serverName(r.Host),
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
		// A header spelled with "_" maps to the same HTTP_* name as its "-"
		// spelling, so a client could add X_Forwarded_For next to the one a
		// trusted proxy set and let map order pick which one PHP sees.
		// Apache and nginx drop such headers from the CGI environment; so do we.
		if strings.Contains(key, "_") {
			continue
		}
		upper := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		if upper == "CONTENT_TYPE" || upper == "CONTENT_LENGTH" {
			continue
		}
		// Block PHP_ADMIN_VALUE/PHP_VALUE injection via HTTP headers
		if strings.HasPrefix(upper, "PHP_") {
			continue
		}
		// httpoxy (CVE-2016-5385): a client-supplied "Proxy" header would
		// become HTTP_PROXY, which Guzzle/cURL-based PHP honors for outbound
		// requests — letting a client redirect the app's server-side traffic.
		// The Proxy header has no legitimate request semantics; drop it.
		if upper == "PROXY" {
			continue
		}
		sep := ", "
		if upper == "COOKIE" {
			// PHP splits cookies at ";" only (F26101).
			sep = "; "
		}
		env["HTTP_"+upper] = strings.Join(vals, sep)
	}

	// PATH_TRANSLATED: document root + path info
	if pathInfo != "" {
		env["PATH_TRANSLATED"] = strings.TrimRight(ctx.DocumentRoot, "/") + pathInfo
	}

	// Custom per-domain environment variables
	for k, v := range customEnv {
		env[k] = v
	}

	// PHP isolation: enforce open_basedir per-request via PHP_ADMIN_VALUE.
	// This works with both system PHP-FPM and UWAS-managed php-cgi.
	// Restricts PHP to domain's web root + optional project root (parent of
	// well-known public dirs) + domain .tmp + system paths.
	// Parent is included only when DocumentRoot looks like a framework public/
	// subdirectory so flat multi-tenant siblings under /var/www stay isolated.
	if ctx.DocumentRoot != "" {
		// Each directory ends in "/": PHP treats an open_basedir entry as a
		// string prefix, so "/var/www/site" would also admit "/var/www/site.au"
		// (F11500).
		docRoot := basedirDir(ctx.DocumentRoot)
		domainTmp := basedirDir(filepath.Join(ctx.DocumentRoot, ".tmp"))
		basedirPaths := docRoot + ":" + domainTmp + ":/usr/share/php:/usr/share/pear:/etc/ssl/certs:/dev/urandom"
		if projectRoot := frameworkProjectRoot(ctx.DocumentRoot); projectRoot != "" {
			basedirPaths = docRoot + ":" + basedirDir(projectRoot) + ":" + domainTmp + ":/usr/share/php:/usr/share/pear:/etc/ssl/certs:/dev/urandom"
		}
		// Apply any pre-existing PHP_ADMIN_VALUE first, then UWAS sandbox last
		// so PHP last-wins keeps open_basedir/disable_functions under UWAS control.
		adminValues := filterCustomPHPAdminValue(env["PHP_ADMIN_VALUE"])
		adminValues = append(adminValues, "open_basedir = "+basedirPaths)
		adminValues = append(adminValues, uploadLimitDirectives(maxUpload)...)
		env["PHP_ADMIN_VALUE"] = strings.Join(adminValues, "\n")
	}

	// php.max_upload applies even without a document root, where the
	// open_basedir block above does not run.
	if _, hasRoot := env["PHP_ADMIN_VALUE"]; !hasRoot {
		if limits := uploadLimitDirectives(maxUpload); len(limits) > 0 {
			env["PHP_ADMIN_VALUE"] = strings.Join(limits, "\n")
		}
	}

	// Remove empty values
	for k, v := range env {
		if v == "" {
			delete(env, k)
		}
	}

	return env
}

// serverName returns the Host without its port, as Apache sets SERVER_NAME;
// the port is reported separately in SERVER_PORT.
func serverName(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
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
	origPath = cgiPath(origPath)

	// If we have a resolved filesystem path, derive SCRIPT_NAME from it
	if resolvedPath != "" {
		if rel, ok := pathsafe.RelativeToBase(docRoot, resolvedPath); ok {
			scriptName = filepath.ToSlash(rel)
			if !strings.HasPrefix(scriptName, "/") {
				scriptName = "/" + scriptName
			}
		}
	}

	// An explicit path suffix belongs to the resolved script, not a rewrite.
	if hasPHPExt(scriptName) && strings.HasPrefix(origPath, scriptName+"/") {
		return scriptName, origPath[len(scriptName):]
	}

	// If resolved path contains .php in the middle of the original URI,
	// split there: /index.php/api/users → scriptName=/index.php, pathInfo=/api/users
	if scriptName == "" {
		parts := strings.Split(origPath, "/")
		for i, part := range parts {
			if hasPHPExt(part) {
				scriptName = strings.Join(parts[:i+1], "/")
				if i+1 < len(parts) {
					pathInfo = "/" + strings.Join(parts[i+1:], "/")
				}
				return scriptName, pathInfo
			}
		}
	}

	// If resolved to a .php file and original URI is different → front-controller
	// But NOT if it's a directory index resolution (e.g., /wp-admin/ → /wp-admin/index.php)
	if hasPHPExt(scriptName) && scriptName != origPath && origPath != "/" {
		// Check if this is a directory index (origPath is a prefix of scriptName)
		cleanOrig := strings.TrimSuffix(origPath, "/")
		if cleanOrig != "" && strings.HasPrefix(scriptName, cleanOrig+"/") {
			// Directory index resolution: /wp-admin/ → /wp-admin/index.php
			// No PATH_INFO needed
			return scriptName, ""
		}
		// Front-controller: /blog/my-post → /index.php
		pathInfo = origPath
		return scriptName, pathInfo
	}

	// Direct .php request (no rewrite): /wp-admin/admin.php
	if hasPHPExt(scriptName) {
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

// cgiPath turns the raw request path into the form CGI expects for PATH_INFO
// and PATH_TRANSLATED: percent-decoded and with dot segments resolved, as
// Apache and nginx deliver it. The raw path went through untouched, so a
// request for "/blog/../../../etc/passwd" reached PHP with a PATH_TRANSLATED
// that points outside the document root (F8400). A path that does not decode
// cleanly, or that decodes to control characters, is left as sent (but still
// has its dot segments resolved).
func cgiPath(p string) string {
	if dec, err := url.PathUnescape(p); err == nil && !hasControlChar(dec) {
		p = dec
	}
	if p == "" {
		return p
	}
	trailing := strings.HasSuffix(p, "/")
	p = path.Clean(p)
	if trailing && p != "/" {
		p += "/"
	}
	return p
}

func hasControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
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

// uploadLimitDirectives renders php.max_upload as php.ini directives.
//
// max_upload was the one field in PHPConfig that nothing read: defaulted in
// defaults.go, merged, documented in SPECIFICATION.md, and never applied. A
// domain configured for 64MB uploads got whatever the system php.ini said,
// which is 2M by PHP's own default.
//
// post_max_size gets headroom over upload_max_filesize because PHP measures
// the whole request body against it: multipart boundaries and the other form
// fields ride along with the file, so setting the two equal would reject an
// upload of exactly the configured size — repeating in miniature the bug this
// fixes.
func uploadLimitDirectives(maxUpload config.ByteSize) []string {
	if maxUpload <= 0 {
		return nil
	}
	postMax := maxUpload + config.MB
	return []string{
		fmt.Sprintf("upload_max_filesize = %d", int64(maxUpload)),
		fmt.Sprintf("post_max_size = %d", int64(postMax)),
	}
}

// frameworkProjectRoot returns the parent of DocumentRoot when it looks like a
// framework public subdirectory (Laravel public/, Symfony public/, etc.).
// Flat multi-tenant roots like /var/www/a.com must NOT include /var/www.
func frameworkProjectRoot(docRoot string) string {
	base := strings.ToLower(filepath.Base(docRoot))
	switch base {
	case "public", "web", "html", "htdocs":
		parent := filepath.Dir(docRoot)
		if parent == "" || parent == "." || parent == string(filepath.Separator) {
			return ""
		}
		// A root such as /var/www/html (the Apache default, and what an Apache
		// import produces) sits directly in the directory every tenant's site
		// lives in; admitting that parent would open all of them (F42600).
		if sharedWebBases[filepath.Clean(parent)] {
			return ""
		}
		return parent
	default:
		return ""
	}
}

// sharedWebBases are directories that hold many sites (or the whole system)
// rather than one project, so they never count as a framework project root.
var sharedWebBases = map[string]bool{
	"/var": true, "/var/www": true, "/srv": true, "/srv/www": true,
	"/home": true, "/opt": true, "/usr": true, "/usr/share": true,
	"/usr/share/nginx": true, "/usr/local": true, "/mnt": true, "/media": true,
	"/etc": true, "/root": true, "/tmp": true,
}

// filterCustomPHPAdminValue keeps safe custom PHP_ADMIN_VALUE lines and drops
// directives that could override UWAS sandbox controls via PHP last-wins. It
// applies the same allowlist as the generated pool ini (phpmanager's
// INIOverrideAllowed), so sendmail_path, ffi.preload, error_log, session.save_path
// and the other PHP_INI_SYSTEM paths a per-domain override may not set are dropped
// here too (F12001). PHP's ini scanner ends an entry at "\r" as well as "\n",
// so both split lines: "memory_limit=1M\rdisable_functions=" is two entries.
func filterCustomPHPAdminValue(existing string) []string {
	if strings.TrimSpace(existing) == "" {
		return nil
	}
	var out []string
	for _, line := range strings.FieldsFunc(existing, func(r rune) bool { return r == '\n' || r == '\r' }) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok || !phpmanager.INIOverrideAllowed(strings.TrimSpace(key), strings.TrimSpace(value)) {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// hasPHPExt reports whether name ends in ".php", ignoring case.
func hasPHPExt(name string) bool {
	return len(name) >= 4 && strings.EqualFold(name[len(name)-4:], ".php")
}

// basedirDir returns dir with exactly one trailing separator.
func basedirDir(dir string) string {
	return strings.TrimRight(dir, "/") + "/"
}
