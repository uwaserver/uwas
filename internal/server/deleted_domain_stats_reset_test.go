package server

// TestDeletedDomainStatsNotInheritedByNewTenant pins F760: a domain deleted
// through the admin API kept its analytics (paths, referrers, unique IPs) and
// per-domain metrics, so a tenant given the re-added hostname read the
// previous owner's data through the tenant-scoped analytics API.

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

func TestDeletedDomainStatsNotInheritedByNewTenant(t *testing.T) {
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
	apiKey := "f760-admin-key-0123456789abcdefABCDEF"
	var b strings.Builder
	fmt.Fprintf(&b, "global:\n  log_level: error\n  log_format: text\n  http_listen: %s\n  web_root: %s\n  users:\n    enabled: true\n  admin:\n    enabled: true\n    listen: %s\n    api_key: %s\ndomains:\n",
		httpAddr, dir, adminAddr, apiKey)
	for _, h := range []string{"a.test", "c.test"} {
		root := filepath.Join(dir, h, "public_html")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "  - host: %s\n    type: static\n    root: %s\n    ssl:\n      mode: \"off\"\n", h, root)
	}
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

	api := func(method, path, key string, body any) (int, []byte) {
		var rd io.Reader
		if body != nil {
			j, _ := json.Marshal(body)
			rd = bytes.NewReader(j)
		}
		req, _ := http.NewRequest(method, "http://"+adminAddr+path, rd)
		req.Header.Set("Authorization", "Bearer "+key)
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

	// 1. The first owner's visitors hit a private path on a.test.
	req, _ := http.NewRequest(http.MethodGet, "http://"+httpAddr+"/alice-private-report.html", nil)
	req.Host = "a.test"
	req.Header.Set("Referer", "https://alice-internal.example/dashboard")
	req.Header.Set("User-Agent", "Mozilla/5.0 Firefox/120.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET a.test: %v", err)
	}
	resp.Body.Close()
	// Recording runs in the request's deferred epilogue; wait for it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if snap := s.analytics.GetHost("a.test"); snap != nil && snap.PageViews >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first request was never recorded")
		}
		runtime.Gosched()
	}

	// 2. Admin deletes a.test, then re-adds it for a new tenant.
	if code, out := api(http.MethodDelete, "/api/v1/domains/a.test?confirm=true", apiKey, nil); code != http.StatusOK {
		t.Fatalf("DELETE a.test = %d %s", code, out)
	}
	if code, out := api(http.MethodPost, "/api/v1/domains", apiKey, map[string]any{
		"host": "a.test", "type": "static", "ssl": map[string]string{"mode": "off"},
	}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("re-add a.test = %d %s", code, out)
	}
	code, out := api(http.MethodPost, "/api/v1/auth/users", apiKey, map[string]any{
		"username": "bob", "email": "bob@example.test", "password": "Bob-Passw0rd-Long!x",
		"role": "user", "domains": []string{"a.test", "c.test"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create bob = %d %s", code, out)
	}
	var bob struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(out, &bob); err != nil || bob.APIKey == "" {
		t.Fatalf("bob api key: %v %s", err, out)
	}

	failures := 0

	// Control: a domain bob owns that never served a request.
	ccode, cbody := api(http.MethodGet, "/api/v1/analytics/c.test", bob.APIKey, nil)
	if ccode != http.StatusNotFound {
		fmt.Printf("CONTROL INVALID: c.test analytics = %d %s\n", ccode, cbody)
		t.Fatal("INVALID PROOF")
	}
	fmt.Println("CONTROL OK: never-served c.test -> 404")

	// Case 1: tenant-scoped detail for the re-added host.
	acode, abody := api(http.MethodGet, "/api/v1/analytics/a.test", bob.APIKey, nil)
	leaked := acode == http.StatusOK && (strings.Contains(string(abody), "alice-private-report") || strings.Contains(string(abody), "alice-internal.example"))
	fmt.Printf("CASE detail: EXPECTED: 404 / no previous-owner data  ACTUAL: %d leaked=%v\n", acode, leaked)
	if leaked || acode == http.StatusOK {
		failures++
	}

	// Case 2: tenant-scoped list.
	_, lbody := api(http.MethodGet, "/api/v1/analytics", bob.APIKey, nil)
	listLeak := strings.Contains(string(lbody), "alice-private-report")
	fmt.Printf("CASE list: EXPECTED: no previous-owner paths  ACTUAL: leaked=%v\n", listLeak)
	if listLeak {
		failures++
	}

	// Case 3: per-domain request metrics carried over.
	carried := s.metrics.DomainStatsSnapshot()["a.test"]["requests"]
	fmt.Printf("CASE metrics: EXPECTED: requests=0  ACTUAL: requests=%d\n", carried)
	if carried != 0 {
		failures++
	}

	if failures > 0 {
		fmt.Printf("PROBLEM CONFIRMED (%d)\n", failures)
		t.FailNow()
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
}
