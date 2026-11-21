package admin

// F65400: DELETE ?cleanup=true on a "www." record removed cron jobs and cron
// monitor history by the www-stripped key, while jobs are labelled with the
// stored host, so the deleted domain's jobs kept running.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
)

// fileBackedCrontab installs a crontab on PATH that keeps its table in a file.
func fileBackedCrontab(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	tab := filepath.Join(dir, "tab")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-l\" ]; then if [ -f '" + tab + "' ]; then cat '" + tab + "'; exit 0; fi; echo 'no crontab for test' >&2; exit 1; fi\n" +
		"cp \"$1\" '" + tab + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "crontab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDeleteWWWRecordCleanupRemovesCronJobs(t *testing.T) {
	fileBackedCrontab(t)
	webRoot := t.TempDir()
	var doms []config.Domain
	for _, h := range []string{"www.a.test", "www.b.test", "plain.test", "keep.test"} {
		doms = append(doms, config.Domain{Host: h, Root: webRoot + "/" + h + "/public_html", Type: "static", SSL: config.SSLConfig{Mode: "off"}})
	}
	s := testServerFromConfig(t, &config.Config{Global: config.GlobalConfig{WebRoot: webRoot}, Domains: doms})
	mon := cronjob.NewMonitor(t.TempDir())
	s.SetCronMonitor(mon)
	for _, l := range []string{"www.a.test", "www.b.test", "b.test", "plain.test", "keep.test"} {
		if err := cronjob.Add(cronjob.Job{Schedule: "@hourly", Command: "echo " + l, Domain: l}); err != nil {
			t.Fatalf("seed %s: %v", l, err)
		}
		mon.RecordExecution(cronjob.ExecutionRecord{ID: l, Domain: l, Command: "echo " + l, Schedule: "@hourly", StartedAt: time.Unix(1, 0), EndedAt: time.Unix(2, 0), Success: true})
	}
	jobsFor := func(label string) int {
		jobs, err := cronjob.List()
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		n := 0
		for _, j := range jobs {
			if j.Domain == label {
				n++
			}
		}
		return n
	}
	for _, path := range []string{"www.a.test", "b.test", "plain.test"} {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/"+path+"?confirm=true&cleanup=true", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("delete %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, l := range []string{"www.a.test", "www.b.test", "b.test", "plain.test"} {
		if n := jobsFor(l); n != 0 {
			t.Errorf("%s: %d cron jobs left after delete with cleanup", l, n)
		}
		if h := mon.GetDomainStatus(l); len(h) != 0 {
			t.Errorf("%s: cron history left after delete with cleanup", l)
		}
	}
	if jobsFor("keep.test") != 1 || len(mon.GetDomainStatus("keep.test")) != 1 {
		t.Error("unrelated keep.test lost its cron job or history")
	}
}
