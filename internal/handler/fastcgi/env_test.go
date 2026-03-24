package fastcgi

import (
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

func TestBuildEnv(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php?page=1", nil)
	r.Header.Set("User-Agent", "TestBrowser")
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.DocumentRoot = "/var/www/html"

	env := BuildEnv(ctx, "/var/www/html/index.php", "/index.php", "", nil)

	checks := map[string]string{
		"REQUEST_METHOD":  "GET",
		"QUERY_STRING":    "page=1",
		"SCRIPT_FILENAME": "/var/www/html/index.php",
		"SCRIPT_NAME":     "/index.php",
		"DOCUMENT_ROOT":   "/var/www/html",
		"SERVER_PROTOCOL": "HTTP/1.1",
		"HTTP_USER_AGENT": "TestBrowser",
		"HTTP_ACCEPT":     "text/html",
	}

	for k, want := range checks {
		if env[k] != want {
			t.Errorf("env[%q] = %q, want %q", k, env[k], want)
		}
	}
}

func TestBuildEnvHTTPS(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.IsHTTPS = true
	ctx.DocumentRoot = "/var/www"

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil)

	if env["HTTPS"] != "on" {
		t.Errorf("HTTPS = %q, want on", env["HTTPS"])
	}
	if env["SERVER_PORT"] != "443" {
		t.Errorf("SERVER_PORT = %q, want 443", env["SERVER_PORT"])
	}
}

func TestBuildEnvCustom(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"

	custom := map[string]string{
		"APP_ENV": "production",
		"DB_HOST": "localhost",
	}

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", custom)

	if env["APP_ENV"] != "production" {
		t.Errorf("APP_ENV = %q, want production", env["APP_ENV"])
	}
	if env["DB_HOST"] != "localhost" {
		t.Errorf("DB_HOST = %q, want localhost", env["DB_HOST"])
	}
}

func TestBuildEnvPOST(t *testing.T) {
	r := httptest.NewRequest("POST", "/submit.php", nil)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Content-Length", "42")
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"

	env := BuildEnv(ctx, "/var/www/submit.php", "/submit.php", "", nil)

	if env["REQUEST_METHOD"] != "POST" {
		t.Errorf("REQUEST_METHOD = %q, want POST", env["REQUEST_METHOD"])
	}
	if env["CONTENT_TYPE"] != "application/x-www-form-urlencoded" {
		t.Errorf("CONTENT_TYPE = %q", env["CONTENT_TYPE"])
	}
	if env["CONTENT_LENGTH"] != "42" {
		t.Errorf("CONTENT_LENGTH = %q, want 42", env["CONTENT_LENGTH"])
	}
}

func TestSplitScriptPath(t *testing.T) {
	tests := []struct {
		name         string
		originalURI  string
		resolvedPath string
		wantScript   string
		wantPath     string
	}{
		{"direct php", "/index.php", "/var/www/index.php", "/index.php", ""},
		{"path info", "/index.php/controller/action", "", "/index.php", "/controller/action"},
		{"front-controller rewrite", "/about", "/var/www/index.php", "/index.php", "/about"},
		{"root", "/", "/var/www/index.php", "/index.php", ""},
		{"wp-admin", "/wp-admin/admin.php", "/var/www/wp-admin/admin.php", "/wp-admin/admin.php", ""},
		{"wordpress pretty", "/blog/my-post/", "/var/www/index.php", "/index.php", "/blog/my-post/"},
		{"rest api", "/wp-json/wp/v2/posts", "/var/www/index.php", "/index.php", "/wp-json/wp/v2/posts"},
		{"query string stripped", "/page?id=5", "/var/www/index.php", "/index.php", "/page"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, pathInfo := SplitScriptPath(tt.originalURI, tt.resolvedPath, "/var/www", []string{"index.php"})
			if script != tt.wantScript {
				t.Errorf("script = %q, want %q", script, tt.wantScript)
			}
			if pathInfo != tt.wantPath {
				t.Errorf("pathInfo = %q, want %q", pathInfo, tt.wantPath)
			}
		})
	}
}

func TestScriptFilename(t *testing.T) {
	got := ScriptFilename("/var/www/html", "/index.php")
	if got != "/var/www/html/index.php" {
		t.Errorf("ScriptFilename = %q, want /var/www/html/index.php", got)
	}
}

// --- Handler coverage tests ---

func TestHandlerNew(t *testing.T) {
	log := logger.New("error", "text")
	h := New(log)
	if h == nil {
		t.Fatal("New() returned nil")
	}
}

func TestBuildEnvRemoteIPOverride(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php", nil)
	r.RemoteAddr = "192.168.1.50:12345"
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.DocumentRoot = "/var/www"
	ctx.RemoteIP = "10.0.0.1" // override from trusted proxy header

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil)

	if env["REMOTE_ADDR"] != "10.0.0.1" {
		t.Errorf("REMOTE_ADDR = %q, want 10.0.0.1", env["REMOTE_ADDR"])
	}
}

func TestSplitScriptPathNoIndexFiles(t *testing.T) {
	// When no index files are provided and URI doesn't contain .php,
	// SplitScriptPath should return the URI as-is with no pathInfo.
	tests := []struct {
		uri        string
		wantScript string
		wantPath   string
	}{
		{"/about", "/about", ""},
		{"/", "/", ""},
		{"/some/page", "/some/page", ""},
	}

	for _, tt := range tests {
		script, pathInfo := SplitScriptPath(tt.uri, "", "/var/www", nil)
		if script != tt.wantScript {
			t.Errorf("SplitScriptPath(%q, nil) script = %q, want %q", tt.uri, script, tt.wantScript)
		}
		if pathInfo != tt.wantPath {
			t.Errorf("SplitScriptPath(%q, nil) pathInfo = %q, want %q", tt.uri, pathInfo, tt.wantPath)
		}
	}
}

func TestClientIPMalformed(t *testing.T) {
	// clientIP with a malformed RemoteAddr (no port) should return the input as-is
	got := clientIP("not-a-host-port")
	if got != "not-a-host-port" {
		t.Errorf("clientIP(malformed) = %q, want %q", got, "not-a-host-port")
	}

	// clientIP with a valid addr
	got = clientIP("192.168.1.1:8080")
	if got != "192.168.1.1" {
		t.Errorf("clientIP(valid) = %q, want 192.168.1.1", got)
	}
}

func TestClientPortMalformed(t *testing.T) {
	// clientPort with a malformed RemoteAddr should return ""
	got := clientPort("not-a-host-port")
	if got != "" {
		t.Errorf("clientPort(malformed) = %q, want empty string", got)
	}

	// clientPort with a valid addr
	got = clientPort("192.168.1.1:9090")
	if got != "9090" {
		t.Errorf("clientPort(valid) = %q, want 9090", got)
	}
}

func TestBuildEnvMalformedRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php", nil)
	r.RemoteAddr = "malformed-addr"
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.DocumentRoot = "/var/www"

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil)

	// With malformed addr, REMOTE_ADDR should be the raw string
	if env["REMOTE_ADDR"] != "malformed-addr" {
		t.Errorf("REMOTE_ADDR = %q, want malformed-addr", env["REMOTE_ADDR"])
	}
	// REMOTE_PORT should be empty (deleted because BuildEnv removes empty values)
	if _, ok := env["REMOTE_PORT"]; ok {
		t.Errorf("REMOTE_PORT should not be set for malformed addr, got %q", env["REMOTE_PORT"])
	}
}
