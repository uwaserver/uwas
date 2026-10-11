package server

// TestDeletedDomainCacheNotInheritedByNewTenant pins F2680: a deleted
// domain's cached responses were served to the next tenant given the same
// hostname until the TTL ran out.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestDeletedDomainCacheNotInheritedByNewTenant(t *testing.T) {
	freeAddr := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().String()
	}
	waitTCP := func(addr string) {
		deadline := time.Now().Add(60 * time.Second)
		for {
			c, err := net.Dial("tcp", addr)
			if err == nil {
				c.Close()
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never became reachable: %v", addr, err)
			}
			runtime.Gosched()
		}
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	httpAddr, adminAddr := freeAddr(), freeAddr()
	apiKey := "f2680-admin-key-0123456789abcdefABCDEF"
	aliceRoot := filepath.Join(dir, "alice", "public_html")
	bobRoot := filepath.Join(dir, "bob", "public_html")
	for _, p := range []string{aliceRoot, bobRoot} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(aliceRoot, "page.html"), []byte("ALICE-PRIVATE"), 0o644)
	os.WriteFile(filepath.Join(bobRoot, "page.html"), []byte("BOB-CONTENT"), 0o644)
	var b strings.Builder
	fmt.Fprintf(&b, "global:\n  log_level: error\n  log_format: text\n  http_listen: %s\n  web_root: %s\n  cache:\n    enabled: true\n    disk_path: %s\n    memory_limit: 16MB\n    default_ttl: 3600\n  admin:\n    enabled: true\n    listen: %s\n    api_key: %s\ndomains:\n",
		httpAddr, dir, filepath.Join(dir, "cache"), adminAddr, apiKey)
	fmt.Fprintf(&b, "  - host: a.test\n    type: static\n    root: %s\n    ssl:\n      mode: \"off\"\n    cache:\n      enabled: true\n      ttl: 3600\n", aliceRoot)
	fmt.Fprintf(&b, "  - host: c.test\n    type: static\n    root: %s\n    ssl:\n      mode: \"off\"\n    cache:\n      enabled: true\n      ttl: 3600\n", bobRoot)
	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	s := New(cfg, logger.New("error", "text"))
	s.SetConfigPath(cfgPath)
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	t.Cleanup(func() {
		s.cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Errorf("Start did not return after cancel")
		}
	})
	waitTCP(httpAddr)
	waitTCP(adminAddr)

	get := func(host, path string) (int, string, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+httpAddr+path, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s%s: %v", host, path, err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out), resp.Header.Get("X-Cache")
	}
	api := func(method, path string, body any) (int, []byte) {
		var rd io.Reader
		if body != nil {
			j, _ := json.Marshal(body)
			rd = bytes.NewReader(j)
		}
		req, _ := http.NewRequest(method, "http://"+adminAddr+path, rd)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, out
	}

	// Warm the cache for a.test (first owner) and prove it is a HIT.
	get("a.test", "/page.html")
	_, body, xc := get("a.test", "/page.html")
	if body != "ALICE-PRIVATE" || xc != "HIT" {
		fmt.Printf("CONTROL INVALID: a.test warm = %q X-Cache=%q\n", body, xc)
		t.Fatal("INVALID PROOF")
	}
	fmt.Println("CONTROL OK: a.test cached ALICE-PRIVATE (HIT)")

	if code, out := api(http.MethodDelete, "/api/v1/domains/a.test?confirm=true", nil); code != http.StatusOK {
		t.Fatalf("DELETE a.test = %d %s", code, out)
	}
	if code, out := api(http.MethodPost, "/api/v1/domains", map[string]any{
		"host": "a.test", "type": "static", "root": bobRoot,
		"ssl": map[string]string{"mode": "off"}, "cache": map[string]any{"enabled": true, "ttl": 3600},
	}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("re-add a.test = %d %s", code, out)
	}

	// Control: c.test (never deleted) serves its own file.
	if _, cb, _ := get("c.test", "/page.html"); cb != "BOB-CONTENT" {
		fmt.Printf("CONTROL INVALID: c.test = %q\n", cb)
		t.Fatal("INVALID PROOF")
	}
	code, got, xc2 := get("a.test", "/page.html")
	fmt.Printf("EXPECTED: BOB-CONTENT (new tenant's file)\nACTUAL: status=%d body=%q X-Cache=%q\n", code, got, xc2)
	if got != "BOB-CONTENT" {
		fmt.Println("PROBLEM CONFIRMED")
		t.FailNow()
	}

	// A host that stayed configured keeps its cached entry.
	get("c.test", "/page.html")
	if _, _, xc := get("c.test", "/page.html"); xc != "HIT" {
		t.Errorf("c.test lost its cache entry: X-Cache=%q", xc)
	}
	// Second delete + re-add cycle: the new tenant's cached page must not
	// survive either.
	get("a.test", "/page.html")
	if _, _, xc := get("a.test", "/page.html"); xc != "HIT" {
		t.Fatalf("a.test second owner not cached: X-Cache=%q", xc)
	}
	api(http.MethodDelete, "/api/v1/domains/a.test?confirm=true", nil)
	os.WriteFile(filepath.Join(aliceRoot, "page.html"), []byte("ALICE-AGAIN"), 0o644)
	api(http.MethodPost, "/api/v1/domains", map[string]any{
		"host": "a.test", "type": "static", "root": aliceRoot,
		"ssl": map[string]string{"mode": "off"}, "cache": map[string]any{"enabled": true, "ttl": 3600},
	})
	if _, got, _ := get("a.test", "/page.html"); got != "ALICE-AGAIN" {
		t.Errorf("second cycle served %q, want ALICE-AGAIN", got)
	}
	fmt.Println("FIX VERIFIED")
	fmt.Println("PROBLEM NOT REPRODUCED")
}
