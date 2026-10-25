package settings

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// loadDeps behaves like the real server: the raw editor writes the file and
// Reload loads it back through config.Load (main file + domains_dir merged).
type loadDeps struct {
	stubDeps
	path string
}

func (d *loadDeps) ConfigPath() string { return d.path }
func (d *loadDeps) AtomicWriteFile(p string, data []byte, perm os.FileMode) error {
	return os.WriteFile(p, data, perm)
}
func (d *loadDeps) Reload() error { _, err := config.Load(d.path); return err }

const rawBase = "global:\n  web_root: /var/www\n  log_level: info\n  admin:\n    enabled: false\n"

// The editor probes only the file it is given, but config.Load also merges
// domains_dir and validates the union. A file that passes the probe and then
// fails the load must not stay on disk: the running server keeps the old
// config while the next start (or reload) fails on the saved file.
func TestConfigRawPutRestoresFileWhenReloadFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(path, []byte(rawBase), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "domains.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	dom := "host: a.example\ntype: static\nroot: /var/www/a\nssl:\n  mode: \"off\"\n"
	if err := os.WriteFile(filepath.Join(dir, "domains.d", "a.yaml"), []byte(dom), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("control: base config must load: %v", err)
	}

	h := New(&loadDeps{path: path})
	body := `{"content":` + jsonQuote(rawBase+"domains:\n  - host: a.example\n    type: static\n    root: /var/www/a2\n    ssl:\n      mode: \"off\"\n") + `}`
	rec := httptest.NewRecorder()
	h.ConfigRawPut(rec, httptest.NewRequest(http.MethodPut, "/api/v1/config/raw", strings.NewReader(body)))

	got, _ := os.ReadFile(path)
	t.Logf("status=%d", rec.Code)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "reload failed") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if string(got) != rawBase {
		t.Fatalf("EXPECTED: config file restored; ACTUAL: file left as saved:\n%s", got)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("config on disk no longer loads: %v", err)
	}
}

// Control: a valid edit still saves.
func TestConfigRawPutValidEditStillSaves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(path, []byte(rawBase), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(&loadDeps{path: path})
	next := strings.Replace(rawBase, "log_level: info", "log_level: debug", 1)
	rec := httptest.NewRecorder()
	h.ConfigRawPut(rec, httptest.NewRequest(http.MethodPut, "/api/v1/config/raw", strings.NewReader(`{"content":`+jsonQuote(next)+`}`)))
	got, _ := os.ReadFile(path)
	if rec.Code != http.StatusOK || string(got) != next {
		t.Fatalf("valid edit: code=%d file=%q", rec.Code, got)
	}
}

func jsonQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}
