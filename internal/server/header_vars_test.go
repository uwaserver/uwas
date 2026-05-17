package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestHandleRequestHeaderTransformVariables(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Domains: []config.Domain{{
			Host: "vars.local",
			Root: dir,
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
			Headers: config.HeadersConfig{
				RequestAdd: map[string]string{
					"X-Forwarded-Host": "$host",
				},
				ResponseAdd: map[string]string{
					"X-Client-IP":   "$remote_addr",
					"X-Request-URI": "$uri",
					"X-Trace":       "$request_id",
					"X-Sanitized":   "a\r\nb",
				},
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/page.html?x=1", nil)
	req.Host = "vars.local"
	req.RemoteAddr = "203.0.113.8:55123"
	req.Header.Set("X-Request-ID", "trace-123")
	s.handleRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("X-Client-IP"); got != "203.0.113.8" {
		t.Fatalf("X-Client-IP = %q, want %q", got, "203.0.113.8")
	}
	if got := rec.Header().Get("X-Request-URI"); got != "/page.html?x=1" {
		t.Fatalf("X-Request-URI = %q, want %q", got, "/page.html?x=1")
	}
	if got := rec.Header().Get("X-Trace"); got != "trace-123" {
		t.Fatalf("X-Trace = %q, want %q", got, "trace-123")
	}
	if got := rec.Header().Get("X-Sanitized"); got != "ab" {
		t.Fatalf("X-Sanitized = %q, want %q", got, "ab")
	}
}
