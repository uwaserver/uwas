package cronjob

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gatedExec makes the next Execute block in its command until release is
// called; started is closed once Execute holds the job's overlap guard. Any
// other Execute runs a command that exits at once.
type gatedExec struct {
	started chan struct{}
	r       *os.File
}

func installGatedExec(t *testing.T) *gatedExec {
	t.Helper()
	orig, origGOOS := monitorExecCommandFn, monitorRuntimeGOOS
	t.Cleanup(func() { monitorExecCommandFn = orig; monitorRuntimeGOOS = origGOOS })
	monitorRuntimeGOOS = "linux"
	g := &gatedExec{}
	monitorExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if g.started == nil {
			return exec.Command("sh", "-c", "exit 0")
		}
		cmd := exec.Command("sh", "-c", "read x; echo inflight-output")
		cmd.Stdin = g.r
		close(g.started)
		g.started = nil
		return cmd
	}
	return g
}

func (g *gatedExec) start(t *testing.T, m *Monitor, domain string) (finish func() ExecutionRecord) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	g.r, g.started = r, started
	done := make(chan ExecutionRecord)
	go func() { done <- m.Execute(domain, "* * * * *", "echo job") }()
	<-started
	return func() ExecutionRecord {
		w.Write([]byte("x\n"))
		w.Close()
		rec := <-done
		r.Close()
		return rec
	}
}

// F66800, F66801: a run in flight when its domain is renamed keeps its overlap
// guard under the new name and files its result there.
func TestMonitorRenameDomainCarriesInFlightRun(t *testing.T) {
	g := installGatedExec(t)
	m := NewMonitor(t.TempDir())
	finish := g.start(t, m, "old.com")
	m.RenameDomain("old.com", "new.com")

	if rec := m.Execute("new.com", "* * * * *", "echo job"); !strings.Contains(rec.Error, "already running") {
		t.Fatalf("renamed job started an overlapping run: %+v", rec)
	}
	rec := finish()
	if rec.Domain != "new.com" || len(m.GetDomainStatus("old.com")) != 0 || len(m.GetDomainStatus("new.com")) != 1 {
		t.Fatalf("in-flight result misfiled: rec.Domain=%s old=%d new=%d",
			rec.Domain, len(m.GetDomainStatus("old.com")), len(m.GetDomainStatus("new.com")))
	}
	if rec := m.Execute("new.com", "* * * * *", "echo job"); !rec.Success {
		t.Fatalf("guard not released after the renamed run ended: %+v", rec)
	}
}

// F66802: a run in flight when its domain is deleted does not bring the
// deleted domain's history (and captured output) back.
func TestMonitorClearDomainDropsInFlightRun(t *testing.T) {
	g := installGatedExec(t)
	dir := t.TempDir()
	m := NewMonitor(dir)
	finish := g.start(t, m, "gone.com")
	m.ClearDomain("gone.com")
	finish()

	data, _ := os.ReadFile(filepath.Join(dir, "cron_history.json"))
	if n := len(m.GetDomainStatus("gone.com")); n != 0 || strings.Contains(string(data), "inflight-output") {
		t.Fatalf("deleted domain's history came back: jobs=%d history file=%s", n, data)
	}
	if rec := m.Execute("gone.com", "* * * * *", "echo job"); !rec.Success || len(m.GetDomainStatus("gone.com")) != 1 {
		t.Fatalf("a later run of a re-added domain is not recorded: %+v", rec)
	}
}
