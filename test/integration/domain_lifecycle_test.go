package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// TestDomainLifecycleE2E drives the full domain add → serve → edit → delete
// flow through the real admin API and checks what the HTTP listener serves
// after each step.
func TestDomainLifecycleE2E(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	webRoot := t.TempDir()
	root1 := filepath.Join(webRoot, "lifecycle.test", "public_html")
	root2 := filepath.Join(webRoot, "lifecycle.test", "edited")
	for dir, body := range map[string]string{root1: "<h1>Hello</h1>", root2: "<h1>Edited</h1>"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Global.WebRoot = webRoot // root path validation checks against this
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: root1, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	base, adminBase := startServerWithAdmin(t, cfg)
	client := &http.Client{Timeout: 2 * time.Second}

	serve := func(t *testing.T) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", base+"/", nil)
		req.Host = "lifecycle.test"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	call := func(t *testing.T, method, path string, payload any, want int) {
		t.Helper()
		var body io.Reader
		if payload != nil {
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(data)
		}
		resp, err := client.Do(adminReq(method, adminBase+path, body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s %s: status = %d, want %d: %s", method, path, resp.StatusCode, want, b)
		}
	}

	t.Run("unknown before add", func(t *testing.T) {
		if code, body := serve(t); code == 200 || strings.Contains(body, "Hello") {
			t.Fatalf("lifecycle.test served before it was added: %d %q", code, body)
		}
	})

	t.Run("add and serve", func(t *testing.T) {
		call(t, "POST", "/api/v1/domains", map[string]any{
			"host": "lifecycle.test", "type": "static", "root": root1,
			"ssl": map[string]string{"mode": "off"},
		}, 201)
		if code, body := serve(t); code != 200 || !strings.Contains(body, "Hello") {
			t.Fatalf("after add: %d %q, want 200 Hello", code, body)
		}
	})

	t.Run("edit root and serve", func(t *testing.T) {
		call(t, "PUT", "/api/v1/domains/lifecycle.test", map[string]any{
			"host": "lifecycle.test", "type": "static", "root": root2,
			"ssl": map[string]string{"mode": "off"},
		}, 200)
		if code, body := serve(t); code != 200 || !strings.Contains(body, "Edited") {
			t.Fatalf("after edit: %d %q, want 200 Edited", code, body)
		}
	})

	t.Run("delete and stop serving", func(t *testing.T) {
		call(t, "DELETE", "/api/v1/domains/lifecycle.test?confirm=true", nil, 200)
		if code, body := serve(t); code == 200 || strings.Contains(body, "Edited") {
			t.Fatalf("after delete: %d %q, want the domain no longer served", code, body)
		}
	})
}

// TestDomainAPIPayload tests that the API correctly handles domain payloads.
func TestDomainAPIPayload(t *testing.T) {
	// Test JSON marshaling of domain with PHP config
	d := config.Domain{
		Host: "api.test",
		Type: "php",
		SSL:  config.SSLConfig{Mode: "auto"},
		PHP: config.PHPConfig{
			FPMAddress: "unix:/run/php/php8.3-fpm.sock",
			IndexFiles: []string{"index.php", "index.html"},
		},
		Cache: config.DomainCache{Enabled: true, TTL: 3600},
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded config.Domain
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.PHP.FPMAddress != "unix:/run/php/php8.3-fpm.sock" {
		t.Errorf("FPM address lost in JSON roundtrip: %q", decoded.PHP.FPMAddress)
	}
	if decoded.Cache.TTL != 3600 {
		t.Errorf("cache TTL lost: %d", decoded.Cache.TTL)
	}
	if decoded.Type != "php" {
		t.Errorf("type lost: %q", decoded.Type)
	}
}
