package watchdog

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// A NOTIFY_SOCKET alone (NotifyAccess set, no WatchdogSec) means systemd is
// not watching: withholding pings restarts nothing, so SelfRestart must act.
func TestSelfRestartExitsWhenSystemdHasNoWatchdog(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "notify.sock")
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("NOTIFY_SOCKET", sock)
	t.Setenv("WATCHDOG_PID", "")
	t.Setenv("WATCHDOG_USEC", "")

	w := testWatchdog(t, 1, func(context.Context) error { return errors.New("wedged") })
	w.notifier = NewNotifier()
	w.cfg.SelfRestart = true
	var code atomic.Int64
	code.Store(-1)
	w.exit = func(c int) { code.Store(int64(c)) }

	w.tick(context.Background())
	if code.Load() != 1 {
		t.Fatalf("exit code = %d, want 1: no systemd watchdog will restart a wedged server", code.Load())
	}
}

// Shutdown cancels Run's context; a probe cut short by that is not evidence
// the server is wedged and must not trigger a self-restart mid-shutdown.
func TestCancelledProbeIsNotALivenessFailure(t *testing.T) {
	started := make(chan struct{})
	w := testWatchdog(t, 1, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	w.cfg.SelfRestart = true
	var exited atomic.Bool
	w.exit = func(int) { exited.Store(true) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.tick(ctx); close(done) }()
	<-started
	cancel()
	<-done

	if exited.Load() {
		t.Fatal("a probe cancelled by shutdown must not exit the process")
	}
	if !w.Healthy() || w.fails.Load() != 0 {
		t.Fatalf("healthy=%v fails=%d; a cancelled probe must not count", w.Healthy(), w.fails.Load())
	}
}
