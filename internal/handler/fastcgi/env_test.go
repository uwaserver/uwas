package fastcgi

import (
	"net/http/httptest"
	"testing"

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
		uri        string
		wantScript string
		wantPath   string
	}{
		{"/index.php", "/index.php", ""},
		{"/index.php/controller/action", "/index.php", "/controller/action"},
		{"/about", "/index.php", "/about"},
		{"/", "/index.php", ""},
	}

	for _, tt := range tests {
		script, pathInfo := SplitScriptPath(tt.uri, "/var/www", []string{"index.php"})
		if script != tt.wantScript {
			t.Errorf("SplitScriptPath(%q) script = %q, want %q", tt.uri, script, tt.wantScript)
		}
		if pathInfo != tt.wantPath {
			t.Errorf("SplitScriptPath(%q) pathInfo = %q, want %q", tt.uri, pathInfo, tt.wantPath)
		}
	}
}

func TestScriptFilename(t *testing.T) {
	got := ScriptFilename("/var/www/html", "/index.php")
	if got != "/var/www/html/index.php" {
		t.Errorf("ScriptFilename = %q, want /var/www/html/index.php", got)
	}
}
