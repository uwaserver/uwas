package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func domainFileTestServer(t *testing.T, hosts ...string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	root := filepath.Join(webRoot, "site", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var ds []config.Domain
	for _, h := range hosts {
		ds = append(ds, config.Domain{Host: h, Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}})
	}
	cfgPath := filepath.Join(dir, "uwas.yaml")
	s := testServerFromConfig(t, &config.Config{
		Global:  config.GlobalConfig{WebRoot: webRoot, LogLevel: "info", LogFormat: "text"},
		Domains: ds,
	})
	s.configPath = cfgPath
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	return s, cfgPath
}

func domainFileTestDo(s *Server, method, url, body string) int {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	return rec.Code
}

func domainFileTestReload(t *testing.T, cfgPath string) string {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("persisted config does not load: %v", err)
	}
	var hs []string
	for _, d := range cfg.Domains {
		hs = append(hs, d.Host)
	}
	sort.Strings(hs)
	return fmt.Sprint(hs)
}

// A rename saved the domain under its new host and left the file under the old
// one, so the next start served the old hostname again (F64100).
func TestDomainRenameRemovesOldDomainFile(t *testing.T) {
	s, p := domainFileTestServer(t, "old.example.com")
	if code := domainFileTestDo(s, http.MethodPut, "/api/v1/domains/old.example.com", `{"host":"new.example.com"}`); code != http.StatusOK {
		t.Fatalf("rename status = %d", code)
	}
	if got := domainFileTestReload(t, p); got != "[new.example.com]" {
		t.Fatalf("after rename, reload = %s, want [new.example.com]", got)
	}
	// A case-only spelling of the same host is not a rename: the file stays.
	if code := domainFileTestDo(s, http.MethodPut, "/api/v1/domains/new.example.com", `{"host":"NEW.example.com","cache":{"enabled":true}}`); code != http.StatusOK {
		t.Fatalf("same-host update status = %d", code)
	}
	if got := domainFileTestReload(t, p); got != "[new.example.com]" {
		t.Fatalf("after same-host update, reload = %s, want [new.example.com]", got)
	}
}

// Deleting a record stored under a "www." host looked for the file under the
// www-stripped name and left it, so the deleted site came back (F64101).
func TestDomainDeleteWWWRecordRemovesItsFile(t *testing.T) {
	for _, path := range []string{"www.legacy.example.com", "legacy.example.com"} {
		s, p := domainFileTestServer(t, "www.legacy.example.com", "keep.example.org")
		if code := domainFileTestDo(s, http.MethodDelete, "/api/v1/domains/"+path+"?confirm=true", ""); code != http.StatusOK {
			t.Fatalf("delete %s status = %d", path, code)
		}
		if got := domainFileTestReload(t, p); got != "[keep.example.org]" {
			t.Fatalf("delete via %s: reload = %s, want [keep.example.org]", path, got)
		}
	}
}
