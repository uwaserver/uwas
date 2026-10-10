package cronjob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F1300: cron_history.json holds every job's captured output and sits in the
// shared web root; it must be owner-only, even over an older 0644 file, and a
// save must not leave temp files behind.
func TestHistoryFileOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cron_history.json")
	m := NewMonitor(dir)
	m.RecordExecution(ExecutionRecord{Domain: "a.example", Command: "echo hi", Output: "secret-token"})
	if got := len(NewMonitor(dir).history); got != 1 {
		t.Fatalf("history not reloaded: %d keys", got)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("fresh file: err=%v mode=%v, want 0600", err, st.Mode().Perm())
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	m.RecordExecution(ExecutionRecord{Domain: "a.example", Command: "echo hi", Output: "again"})
	if st, _ := os.Stat(file); st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("existing 0644 file kept mode %v, want 0600", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files after save: %v", entries)
	}
	if n := len(NewMonitor(dir).history["a.example:echo hi"]); n != 2 {
		t.Fatalf("reloaded records = %d, want 2", n)
	}
}

// F1301: a command with runs of spaces must be read back from the crontab
// exactly as written, so dedupe and Remove can match it.
func TestParseCronLinePreservesCommand(t *testing.T) {
	for _, c := range []struct{ line, sched, cmd string }{
		{`0 3 * * * echo "a b"`, "0 3 * * *", `echo "a b"`},
		{`0 3 * * * echo "a  b"`, "0 3 * * *", `echo "a  b"`},
		{`@daily echo "a  b"`, "@daily", `echo "a  b"`},
		{"0 3 * * *\techo x", "0 3 * * *", "echo x"},
		{`0  3 * *  * echo  hi`, "0 3 * * *", "echo  hi"},
		{`  0 3 * * * echo 50\%  done  `, "0 3 * * *", "echo 50%  done"},
	} {
		j := parseCronLine(c.line)
		if j.Command != c.cmd || j.Schedule != c.sched {
			t.Errorf("%q: got sched=%q cmd=%q, want %q / %q", c.line, j.Schedule, j.Command, c.sched, c.cmd)
		}
	}
	// no command / too few fields keep their old behavior
	if j := parseCronLine("@reboot"); j.Command != "@reboot" || j.Schedule != "" {
		t.Errorf("@reboot alone: %+v", j)
	}
	if j := parseCronLine("0 3 * * *"); !strings.HasPrefix(j.Command, "0 3") || j.Schedule != "" {
		t.Errorf("5 fields: %+v", j)
	}
}
