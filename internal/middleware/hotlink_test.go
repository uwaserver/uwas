package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func TestHotlinkProtectionBlocksExternalReferer(t *testing.T) {
	guard := HotlinkGuard(logger.New("error", "text"), []string{"example.com"}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/images/photo.jpg", nil)
	req.Header.Set("Referer", "https://evil.test/page")
	req.Host = "example.com"
	if guard(rec, req) {
		t.Fatal("guard allowed external referer")
	}

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHotlinkProtectionAllowsSameHostAndUnprotectedPath(t *testing.T) {
	guard := HotlinkGuard(logger.New("error", "text"), nil, nil)

	for _, tc := range []struct {
		name    string
		path    string
		referer string
	}{
		{name: "same host", path: "/images/photo.jpg", referer: "https://example.com/page"},
		{name: "unprotected extension", path: "/page.html", referer: "https://evil.test/page"},
		{name: "no referer", path: "/images/photo.jpg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Host = "example.com"
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if !guard(rec, req) {
				t.Fatal("guard blocked allowed request")
			}
		})
	}
}
