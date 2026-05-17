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

func TestHandleRequestHotlinkProtection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Domains: []config.Domain{{
			Host: "hotlink.local",
			Root: dir,
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{
				HotlinkProtection: config.HotlinkConfig{Enabled: true},
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/photo.jpg", nil)
	req.Host = "hotlink.local"
	req.Header.Set("Referer", "https://external.example/page")
	s.handleRequest(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
