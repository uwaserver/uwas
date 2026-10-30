package admin

// F53603: the cron monitor kept a job's execution history (captured output,
// failure streak) for the life of the process after the job was deleted, and
// rewrote all of it to cron_history.json on every run. Deleting a job now
// drops its history; a same-command job on another domain keeps its own.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/cronjob"
)

// crontabWith installs a fake crontab binary whose `-l` prints content and
// whose write path replaces that list, so the host crontab is never touched.
func crontabWith(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	list := filepath.Join(dir, "list")
	if err := os.WriteFile(list, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = \"-l\" ]; then /bin/cat '" + list + "'; exit 0; fi\n/bin/cp \"$1\" '" + list + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "crontab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestCronDeleteDropsMonitorHistory(t *testing.T) {
	crontabWith(t, "# UWAS managed [a.example] job\n*/5 * * * * /bin/true\n# UWAS managed [b.example] job\n*/5 * * * * /bin/true\n")
	s := testServer()
	mon := cronjob.NewMonitor("")
	s.SetCronMonitor(mon)
	mon.RecordExecution(cronjob.ExecutionRecord{Domain: "a.example", Command: "/bin/true", Success: false, Output: "tenant A secret"})
	mon.RecordExecution(cronjob.ExecutionRecord{Domain: "b.example", Command: "/bin/true", Success: true})

	rec := serve(s, "DELETE", "/api/v1/cron", `{"schedule":"*/5 * * * *","command":"/bin/true"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cron delete = %d: %s", rec.Code, rec.Body.String())
	}
	t.Logf("EXPECTED: no history for either removed job\nACTUAL: a=%v b=%v", mon.GetStatus("a.example", "/bin/true") != nil, mon.GetStatus("b.example", "/bin/true") != nil)
	if mon.GetStatus("a.example", "/bin/true") != nil || mon.GetStatus("b.example", "/bin/true") != nil {
		t.Fatal("PROBLEM CONFIRMED: deleted job's history survived")
	}
}

func TestCronDeleteKeepsHistoryOfUnrelatedJob(t *testing.T) {
	crontabWith(t, "# UWAS managed [a.example] job\n*/5 * * * * /bin/true\n")
	s := testServer()
	mon := cronjob.NewMonitor("")
	s.SetCronMonitor(mon)
	mon.RecordExecution(cronjob.ExecutionRecord{Domain: "a.example", Command: "/bin/true", Success: true})
	mon.RecordExecution(cronjob.ExecutionRecord{Domain: "a.example", Command: "/bin/other", Success: true})
	mon.RecordExecution(cronjob.ExecutionRecord{Domain: "c.example", Command: "/bin/true", Success: true})

	rec := serve(s, "DELETE", "/api/v1/cron", `{"schedule":"*/5 * * * *","command":"/bin/true"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cron delete = %d: %s", rec.Code, rec.Body.String())
	}
	if mon.GetStatus("a.example", "/bin/true") != nil {
		t.Fatal("removed job history kept")
	}
	if mon.GetStatus("a.example", "/bin/other") == nil || mon.GetStatus("c.example", "/bin/true") == nil {
		t.Fatal("control: history of jobs that were not removed must stay")
	}
}
