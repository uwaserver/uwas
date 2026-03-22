package admin

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// --- GET /api/v1/config/raw ---

func TestConfigRawGetNoPath(t *testing.T) {
	s := testServer()
	// configPath is empty

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/raw", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestConfigRawGetSuccess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	content := "global:\n  log_level: info\n"
	os.WriteFile(cfgPath, []byte(content), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/raw", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/x-yaml" {
		t.Errorf("Content-Type = %q, want application/x-yaml", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != content {
		t.Errorf("body = %q, want %q", rec.Body.String(), content)
	}
}

func TestConfigRawGetMissingFile(t *testing.T) {
	s := testServer()
	s.SetConfigPath("/nonexistent/uwas.yaml")

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/raw", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// --- PUT /api/v1/config/raw ---

func TestConfigRawPutNoPath(t *testing.T) {
	s := testServer()

	body := strings.NewReader("global:\n  log_level: debug\n")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", body))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestConfigRawPutSuccess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global:\n  log_level: info\n"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	newContent := "global:\n  log_level: debug\n"
	body := strings.NewReader(newContent)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "saved" {
		t.Errorf("status = %q, want saved", resp["status"])
	}

	// Verify the file was actually written.
	data, _ := os.ReadFile(cfgPath)
	if string(data) != newContent {
		t.Errorf("file content = %q, want %q", string(data), newContent)
	}
}

func TestConfigRawPutInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global:\n  log_level: info\n"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	body := strings.NewReader("{{invalid yaml}}")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "invalid YAML") {
		t.Errorf("error = %q, want contains 'invalid YAML'", resp["error"])
	}

	// Verify original file is unchanged.
	data, _ := os.ReadFile(cfgPath)
	if string(data) != "global:\n  log_level: info\n" {
		t.Errorf("file should not have changed, got %q", string(data))
	}
}

func TestConfigRawPutTriggersReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global:\n  log_level: info\n"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	reloaded := false
	s.SetReloadFunc(func() error {
		reloaded = true
		return nil
	})

	body := strings.NewReader("global:\n  log_level: debug\n")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !reloaded {
		t.Error("reload should have been triggered")
	}
}

func TestConfigRawPutReloadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global:\n  log_level: info\n"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)
	s.SetReloadFunc(func() error { return errors.New("reload boom") })

	body := strings.NewReader("global:\n  log_level: debug\n")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", body))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "reload failed") {
		t.Errorf("error = %q, want contains 'reload failed'", resp["error"])
	}
}

// --- GET /api/v1/config/domains/{host}/raw ---

func TestDomainRawGetNoConfigPath(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/domains/example.com/raw", nil))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDomainRawGetSuccess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	domainContent := "host: example.com\nroot: /var/www/example\ntype: static\n"
	os.WriteFile(filepath.Join(domainsDir, "example.com.yaml"), []byte(domainContent), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/domains/example.com/raw", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/x-yaml" {
		t.Errorf("Content-Type = %q, want application/x-yaml", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != domainContent {
		t.Errorf("body = %q, want %q", rec.Body.String(), domainContent)
	}
}

func TestDomainRawGetNotFound(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	os.MkdirAll(filepath.Join(dir, "domains.d"), 0755)

	s := testServer()
	s.SetConfigPath(cfgPath)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/domains/nonexistent.com/raw", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- PUT /api/v1/config/domains/{host}/raw ---

func TestDomainRawPutSuccess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	domainYAML := "host: newsite.com\nroot: /var/www/new\ntype: static\n"
	body := strings.NewReader(domainYAML)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/newsite.com/raw", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "saved" {
		t.Errorf("status = %q, want saved", resp["status"])
	}

	// Verify the file was created.
	path := filepath.Join(dir, "domains.d", "newsite.com.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("domain file not created: %v", err)
	}
	if string(data) != domainYAML {
		t.Errorf("file content = %q, want %q", string(data), domainYAML)
	}
}

func TestDomainRawPutInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	body := strings.NewReader("{{invalid yaml}}")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/example.com/raw", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "invalid YAML") {
		t.Errorf("error = %q, want contains 'invalid YAML'", resp["error"])
	}
}

func TestDomainRawPutTriggersReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	reloaded := false
	s.SetReloadFunc(func() error {
		reloaded = true
		return nil
	})

	domainYAML := "host: example.com\ntype: static\n"
	body := strings.NewReader(domainYAML)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/example.com/raw", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !reloaded {
		t.Error("reload should have been triggered")
	}
}

func TestDomainRawPutWithDomainsDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	customDir := filepath.Join(dir, "custom-domains")

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
		DomainsDir: customDir,
		Domains: []config.Domain{
			{Host: "example.com", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)
	s.SetConfigPath(cfgPath)

	domainYAML := "host: example.com\ntype: static\n"
	body := strings.NewReader(domainYAML)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/example.com/raw", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	// Verify file was created in the custom directory.
	path := filepath.Join(customDir, "example.com.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("domain file not created in custom dir: %v", err)
	}
	if string(data) != domainYAML {
		t.Errorf("file content = %q, want %q", string(data), domainYAML)
	}
}

// --- domainFilePath ---

func TestDomainFilePathSanitization(t *testing.T) {
	s := testServer()
	s.SetConfigPath("/etc/uwas/uwas.yaml")

	tests := []struct {
		host    string
		wantErr bool
	}{
		{"example.com", false},
		{"../etc/passwd", true},
		{"../../root", true},
		{".", true},
		{"..", true},
	}

	for _, tt := range tests {
		_, err := s.domainFilePath(tt.host)
		if tt.wantErr && err == nil {
			t.Errorf("domainFilePath(%q) should return error", tt.host)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("domainFilePath(%q) unexpected error: %v", tt.host, err)
		}
	}
}
