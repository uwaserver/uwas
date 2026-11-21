package admin

// F65401: renaming a domain left its cron jobs and cron history labelled with
// the old host, so DELETE of the renamed domain with cleanup left them running.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
)

func TestRenameDomainMovesCronJobs(t *testing.T) {
	fileBackedCrontab(t)
	webRoot := t.TempDir()
	var doms []config.Domain
	for _, h := range []string{"old.test", "keep.test"} {
		doms = append(doms, config.Domain{Host: h, Root: webRoot + "/" + h + "/public_html", Type: "static", SSL: config.SSLConfig{Mode: "off"}})
	}
	s := testServerFromConfig(t, &config.Config{Global: config.GlobalConfig{WebRoot: webRoot}, Domains: doms})
	mon := cronjob.NewMonitor(t.TempDir())
	s.SetCronMonitor(mon)
	for _, h := range []string{"old.test", "keep.test"} {
		if err := cronjob.Add(cronjob.Job{Schedule: "@daily", Command: "echo " + h, Domain: h}); err != nil {
			t.Fatalf("seed %s: %v", h, err)
		}
		mon.RecordExecution(cronjob.ExecutionRecord{Domain: h, Command: "echo " + h, Success: true})
	}
	do := func(method, path, body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
	}

	do("PUT", "/api/v1/domains/old.test", `{"host":"new.test"}`)
	if mon.GetStatus("new.test", "echo old.test") == nil || mon.GetStatus("old.test", "echo old.test") != nil {
		t.Fatal("cron history did not follow the rename")
	}
	do("DELETE", "/api/v1/domains/new.test?confirm=true&cleanup=true", "")

	jobs, err := cronjob.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Domain != "keep.test" {
		t.Fatalf("jobs after deleting the renamed domain = %+v, want only keep.test's", jobs)
	}
	if mon.GetStatus("new.test", "echo old.test") != nil || mon.GetStatus("keep.test", "echo keep.test") == nil {
		t.Fatal("cron history: renamed domain's kept or another domain's dropped")
	}
}
