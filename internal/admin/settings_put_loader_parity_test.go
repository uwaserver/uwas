package admin

// Regression test for F880: PUT /api/v1/settings persists global values that config.Load
// rejects, so the next start/reload/watchdog restart fails for the whole server.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func settingsParityServer(t *testing.T) (*Server, string) {
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	root := filepath.Join(webRoot, "a.test", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "uwas.yaml")
	s := testServerFromConfig(t, &config.Config{
		Global:  config.GlobalConfig{WebRoot: webRoot, LogLevel: "info", LogFormat: "text"},
		Domains: []config.Domain{{Host: "a.test", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}}},
	})
	s.configPath = cfgPath
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(cfgPath); err != nil {
		t.Fatalf("setup config does not load: %v", err)
	}
	return s, cfgPath
}

func settingsParityPut(s *Server, body string) int {
	r := withAdminContext(httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body)))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	return rec.Code
}

func TestSettingsPutRejectsValuesConfigLoadRejects(t *testing.T) {
	// Control: a valid change is saved and the file still loads.
	s, path := settingsParityServer(t)
	code := settingsParityPut(s, `{"global.log_level":"warn"}`)
	cfg, err := config.Load(path)
	if code != 200 || err != nil || cfg.Global.LogLevel != "warn" {
		fmt.Printf("CONTROL FAILED: code=%d err=%v\n", code, err)
		fmt.Println("INVALID PROOF")
		t.FailNow()
	}
	fmt.Println("CONTROL PASSED: valid log_level saved and loads")

	failures := 0
	for name, body := range map[string]string{
		"log_level verbose":     `{"global.log_level":"verbose"}`,
		"acme.email without @":  `{"global.acme.email":"ops.example.com"}`,
		"http_listen garbage":   `{"global.http_listen":"not-an-address"}`,
		"backup provider ftp":   `{"global.backup.enabled":true,"global.backup.provider":"ftp","global.backup.keep":3}`,
		"trusted_proxies bogus": `{"global.trusted_proxies":"10.0.0.0/8\nnot-a-cidr"}`,
	} {
		s, path := settingsParityServer(t)
		code := settingsParityPut(s, body)
		_, lerr := config.Load(path)
		fmt.Printf("%s: EXPECTED: rejected (4xx) and config still loads   ACTUAL: code=%d load_err=%v\n", name, code, lerr)
		if code < 400 || lerr != nil {
			failures++
		}
	}
	if failures > 0 {
		fmt.Printf("PROBLEM CONFIRMED (%d)\n", failures)
		t.Fail()
		return
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
}
