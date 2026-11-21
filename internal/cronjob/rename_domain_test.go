package cronjob

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// F65401: RenameDomain re-labels only the renamed domain's managed jobs, in
// one crontab write, and leaves operator lines, quoted markers and domains
// sharing a prefix alone.
func TestRenameDomainRelabelsOnlyThatDomain(t *testing.T) {
	origExec, origGOOS := execCommandFn, runtimeGOOS
	defer func() { execCommandFn = origExec; runtimeGOOS = origGOOS }()
	runtimeGOOS = "linux"
	crontab := strings.Join([]string{
		"*/5 * * * * /usr/local/bin/operator",
		"# UWAS managed [a.com] nightly",
		"0 2 * * * /bin/a",
		"# UWAS managed [b.com] see # UWAS managed [a.com] note",
		"0 3 * * * /bin/b",
		"# UWAS managed [a.com.example] ",
		"0 4 * * * /bin/c",
		"",
	}, "\n")
	writes := 0
	var written string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) == 1 && args[0] == "-l" {
			return exec.Command("printf", "%s", crontab)
		}
		if len(args) == 1 {
			d, _ := os.ReadFile(args[0])
			written = string(d)
			writes++
		}
		return exec.Command("true")
	}
	if err := RenameDomain("a.com", "z.com"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(crontab, "# UWAS managed [a.com] nightly", "# UWAS managed [z.com] nightly", 1)
	if writes != 1 || written != want {
		t.Fatalf("writes=%d crontab:\n%s\nwant:\n%s", writes, written, want)
	}

	// Nothing to re-label: no write at all.
	writes = 0
	if err := RenameDomain("none.com", "z.com"); err != nil || writes != 0 {
		t.Fatalf("no-op rename: err=%v writes=%d", err, writes)
	}
	// A label cron would misparse is refused before touching the crontab.
	if err := RenameDomain("a.com", "z]x"); !errors.Is(err, ErrInvalidJob) || writes != 0 {
		t.Fatalf("bad label: err=%v writes=%d", err, writes)
	}
	// A failing `crontab -l` is reported, never overwritten.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) == 1 && args[0] == "-l" {
			return exec.Command("sh", "-c", "echo 'cannot open' >&2; exit 1")
		}
		writes++
		return exec.Command("true")
	}
	if err := RenameDomain("a.com", "z.com"); err == nil || writes != 0 {
		t.Fatalf("read failure: err=%v writes=%d", err, writes)
	}
}

// F65401: the monitor history follows a renamed domain, so ClearDomain on the
// new name releases it.
func TestMonitorRenameDomainMovesHistory(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor(dir)
	m.RecordExecution(ExecutionRecord{Domain: "a.example", Command: "one", Success: true, Output: "A-OUT"})
	m.RecordExecution(ExecutionRecord{Domain: "ab.example", Command: "one", Success: true})

	m.RenameDomain("a.example", "z.example")

	if m.GetStatus("a.example", "one") != nil {
		t.Fatal("history stayed under the old name")
	}
	st := m.GetStatus("z.example", "one")
	if st == nil || len(st.History) != 1 || st.History[0].Domain != "z.example" {
		t.Fatalf("history not moved: %+v", st)
	}
	if m.GetStatus("ab.example", "one") == nil {
		t.Fatal("control: a domain sharing a prefix must keep its history")
	}
	data, err := os.ReadFile(filepath.Join(dir, "cron_history.json"))
	if err != nil || !strings.Contains(string(data), "z.example:one") || strings.Contains(string(data), `"a.example:one"`) {
		t.Fatalf("persisted history not re-keyed: %s (%v)", data, err)
	}
	m.ClearDomain("z.example")
	if m.GetStatus("z.example", "one") != nil {
		t.Fatal("ClearDomain on the new name left the moved history")
	}
}
