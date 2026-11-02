package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// fakeCrontabScript installs a crontab on PATH that fails every call.
func fakeCrontabScript(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crontab"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func deleteWithCleanup(t *testing.T, host string) *httptest.ResponseRecorder {
	t.Helper()
	webRoot := t.TempDir()
	cfg := &config.Config{
		Global:  config.GlobalConfig{WebRoot: webRoot},
		Domains: []config.Domain{{Host: host, Root: webRoot + "/" + host + "/public_html", Type: "static", SSL: config.SSLConfig{Mode: "off"}}},
	}
	s := testServerFromConfig(t, cfg)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/"+host+"?confirm=true&cleanup=true", nil))
	return rec
}

// F63003: ?cleanup=true discarded the errors of the cron-job and SFTP-account
// steps, so a crontab that could not be rewritten left the domain's jobs
// running while the API answered "deleted".
func TestDeleteDomainCleanupReportsCronFailure(t *testing.T) {
	fakeCrontabScript(t, `echo "crontab: cannot write" >&2; exit 1`)
	rec := deleteWithCleanup(t, "gone.test")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "cleanup incomplete") {
		t.Fatalf("EXPECTED: 500 naming the incomplete cleanup; ACTUAL: %d %s", rec.Code, rec.Body.String())
	}
}

// Control: a working crontab (nothing to remove) still answers 200.
func TestDeleteDomainCleanupSucceedsWithWorkingCrontab(t *testing.T) {
	fakeCrontabScript(t, `if [ "$1" = "-l" ]; then echo "no crontab for test" >&2; exit 1; fi; exit 0`)
	rec := deleteWithCleanup(t, "gone.test")
	if rec.Code != http.StatusOK {
		t.Fatalf("EXPECTED: 200; ACTUAL: %d %s", rec.Code, rec.Body.String())
	}
}
