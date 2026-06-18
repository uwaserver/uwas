package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestCloudflareOnlyBlocksDirectOriginHTTP(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("origin"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cloudflare:  config.CloudflareConfig{IPRanges: []string{"203.0.113.0/24"}},
		},
		Domains: []config.Domain{{
			Host: "example.test",
			Root: dir,
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{
				CloudflareOnly: true,
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "example.test"
	req.RemoteAddr = "198.51.100.9:45123"
	req.Header.Set("User-Agent", "test")
	s.handleHTTP(rec, req)

	if rec.Code != 421 {
		t.Fatalf("status = %d, want 421", rec.Code)
	}
	blocked := s.securityStats.RecentBlocked()
	if len(blocked) == 0 || blocked[0].Reason != "cloudflare_only" {
		t.Fatalf("blocked stats = %#v, want cloudflare_only entry", blocked)
	}
}

func TestCloudflareOnlyAllowsCloudflareOriginHTTP(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("origin"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cloudflare:  config.CloudflareConfig{IPRanges: []string{"203.0.113.0/24"}},
		},
		Domains: []config.Domain{{
			Host: "example.test",
			Root: dir,
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{
				CloudflareOnly: true,
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "example.test"
	req.RemoteAddr = "203.0.113.44:45123"
	req.Header.Set("User-Agent", "test")
	s.handleHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "origin") {
		t.Fatalf("body = %q, want origin", rec.Body.String())
	}
}
