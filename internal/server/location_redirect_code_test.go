package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// A location redirect_code that is not a valid HTTP status must not panic the
// request: http.Redirect panics for a code above 999 (F54000).
func TestLocationRedirectInvalidCodeDoesNotPanic(t *testing.T) {
	for _, code := range []int{1000, 5} {
		cfg := &config.Config{
			Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
			Domains: []config.Domain{{
				Host: "redir.example.com", Type: "static", Root: t.TempDir(),
				Locations: []config.LocationConfig{{Match: "/legacy", Redirect: "/modern", RedirectCode: code}},
			}},
		}
		s := newMinimalServer(cfg)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/legacy/x", nil)
		req.Host = "redir.example.com"
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("code %d: handleRequest panicked: %v", code, p)
				}
			}()
			s.handleRequest(rec, req)
		}()
		if rec.Code < 300 || rec.Code > 399 {
			if rec.Code != http.StatusMovedPermanently {
				t.Errorf("code %d: answered %d, want a 3xx", code, rec.Code)
			}
		}
	}
}
