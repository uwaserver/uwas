//go:build linux

package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cttyStart runs body as the session shell; $PIDF and $CHILDF are replaced
// with file paths the script writes pids into.
func cttyStart(t *testing.T, body string) (s *tdSession, pid, childf string) {
	t.Helper()
	dir := t.TempDir()
	pidf := filepath.Join(dir, "pid")
	childf = filepath.Join(dir, "child")
	script := strings.NewReplacer("$PIDF", pidf, "$CHILDF", childf).Replace(body)
	s = tdDial(t, &Handler{Shell: tdScript(t, script)})
	pid = tdWaitFile(t, pidf, 10*time.Second)
	t.Cleanup(func() {
		tdKill(pid)
		s.stop()
		select { // ServeHTTP must finish before tdGrace restores hangupGrace
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Errorf("session did not end during cleanup")
		}
	})
	if got := s.cli.readTextUntil(t, "READY", 10*time.Second); !strings.Contains(got, "READY") {
		t.Fatalf("no READY, got %q", got)
	}
	return s, pid, childf
}

// cttyGone waits (failure bound) until pid has exited or is a zombie.
func cttyGone(pid string, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if !tdAlive(pid) {
			return true
		}
		if b, err := os.ReadFile("/proc/" + pid + "/stat"); err == nil {
			if f := strings.Fields(string(b)); len(f) > 2 && f[2] == "Z" {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestPTYIsControllingTerminal pins F800/F801: the shell owns the PTY as its
// controlling terminal, so Ctrl+C reaches the foreground program and the
// kernel hangs up the session's processes when the session ends.
func TestPTYIsControllingTerminal(t *testing.T) {
	tdGrace(t, 50*time.Millisecond)

	t.Run("ctrl_c_interrupts_foreground", func(t *testing.T) {
		s, _, _ := cttyStart(t, "echo $$ > $PIDF\necho READY\nexec cat\n")
		if err := s.cli.writeFrame([]byte{0x03}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Fatal("Ctrl+C did not interrupt the foreground program")
		}
	})

	t.Run("disconnect_hangs_up_children", func(t *testing.T) {
		s, _, childf := cttyStart(t, "sleep 100000 &\necho $! > $CHILDF\necho $$ > $PIDF\necho READY\nwhile :; do read -r l; done\n")
		child := tdWaitFile(t, childf, 10*time.Second)
		t.Cleanup(func() { tdKill(child) })
		s.cli.conn.Close()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Fatal("session did not end after disconnect")
		}
		if !cttyGone(child, 5*time.Second) {
			t.Fatalf("shell child %s still running after the session ended", child)
		}
	})
}
