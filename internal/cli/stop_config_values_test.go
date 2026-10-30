package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withConfigFile(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	old := findConfigFn
	findConfigFn = func(string) (string, bool) { return p, true }
	t.Cleanup(func() { findConfigFn = old })
}

// F56303: the loader falls back on `${VAR:-x}` when VAR is unset OR empty; the
// CLI's copy only on unset, so with an empty RUNTIME_DIRECTORY the server
// wrote /run/uwas/uwas.pid and `uwas stop` looked for /uwas.pid.
func TestPIDFileEmptyEnvFallsBack(t *testing.T) {
	t.Setenv("UWAS_TEST_RUNDIR", "")
	withConfigFile(t, "global:\n  pid_file: ${UWAS_TEST_RUNDIR:-/run/uwas}/uwas.pid\n")
	if got := pidFileFromConfig(); got != "/run/uwas/uwas.pid" {
		t.Fatalf("pid_file = %q, want /run/uwas/uwas.pid", got)
	}
}

func TestPIDFileSetEnvUsed(t *testing.T) {
	t.Setenv("UWAS_TEST_RUNDIR", "/custom")
	withConfigFile(t, "global:\n  pid_file: ${UWAS_TEST_RUNDIR:-/run/uwas}/uwas.pid\n")
	if got := pidFileFromConfig(); got != "/custom/uwas.pid" {
		t.Fatalf("pid_file = %q", got)
	}
}

// F56304: the server drains for global.timeouts.shutdown_grace (30s by
// default) but `uwas stop` sent SIGKILL after 15s, cutting the drain, the app
// shutdown and the state saves short. The wait now outlasts the grace period.
func TestStopWaitOutlastsShutdownGrace(t *testing.T) {
	withConfigFile(t, "global:\n  pid_file: /tmp/x.pid\n")
	if got := stopWaitFromConfig(); got <= 30*time.Second {
		t.Fatalf("default wait = %v, must exceed the 30s default grace", got)
	}
	withConfigFile(t, "global:\n  timeouts:\n    shutdown_grace: 90s\n")
	if got := stopWaitFromConfig(); got <= 90*time.Second {
		t.Fatalf("wait = %v, must exceed a 90s grace", got)
	}
	withConfigFile(t, "global:\n  timeouts:\n    shutdown_grace: 45\n")
	if got := stopWaitFromConfig(); got <= 45*time.Second {
		t.Fatalf("wait = %v, must exceed a 45s (integer seconds) grace", got)
	}
	withConfigFile(t, "global:\n  timeouts:\n    shutdown_grace: nonsense\n")
	if got := stopWaitFromConfig(); got <= 30*time.Second {
		t.Fatalf("unparsable grace must fall back to the default, got %v", got)
	}
}
