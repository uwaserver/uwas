//go:build linux

package terminal

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// F67501: when the shell exits while the PTY->WS pump is blocked writing to a
// client that stopped reading, Close must still close the socket. It used to
// wait for the pump's write lock, which only the client could release, so the
// session (slot, goroutines, connection) outlived its shell.
//
// Gate: the pump is parked in WriteText ([IO wait]) before the shell is killed.
// The 10s wait is a failure bound; teardown takes about closeWriteTimeout.
func TestServeHTTPEndsWhenShellExitsWithStalledClient(t *testing.T) {
	dir := t.TempDir()
	pidf := filepath.Join(dir, "pid")
	t.Setenv("F67501_PID", pidf)
	s := tdDial(t, &Handler{Shell: tdScript(t, "echo $$ > \"$F67501_PID\"\necho READY\nexec yes\n")})
	pid := tdWaitFile(t, pidf, 10*time.Second)
	t.Cleanup(func() { tdKill(pid); s.stop() })
	if got := s.cli.readTextUntil(t, "READY", 10*time.Second); !strings.Contains(got, "READY") {
		t.Fatalf("no READY: %q", got)
	}
	_ = s.cli.conn.SetReadDeadline(time.Time{})

	// Stop reading: the pump fills the socket buffers and parks holding wmu.
	tdWaitStack(t, 20*time.Second, "[IO wait", "terminal.(*WSConn).WriteText", "net.(*conn).Write")
	tdKill(pid)

	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("ServeHTTP did not return within 10s after the shell exited (client connected, not reading)")
	}
}
