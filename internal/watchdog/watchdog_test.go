package watchdog

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

func testWatchdog(t *testing.T, failures int, probe func(context.Context) error) *Watchdog {
	t.Helper()
	w := &Watchdog{
		cfg:      Config{Enabled: true, Interval: time.Millisecond, Timeout: 50 * time.Millisecond, Failures: failures},
		log:      logger.New("error", "text"),
		notifier: &Notifier{dead: true},
		probe:    probe,
	}
	w.healthy.Store(true)
	return w
}

// The whole point: a healthy server stays healthy, and only a run of failures
// long enough to clear the threshold marks it unresponsive. A single slow
// probe restarting a busy-but-working server would be worse than the outage.
func TestSingleFailureDoesNotMarkUnhealthy(t *testing.T) {
	var fail atomic.Bool
	w := testWatchdog(t, 3, func(context.Context) error {
		if fail.Load() {
			return errors.New("timeout")
		}
		return nil
	})

	w.tick(context.Background())
	if !w.Healthy() {
		t.Fatal("a passing probe must leave the server healthy")
	}

	fail.Store(true)
	w.tick(context.Background())
	if !w.Healthy() {
		t.Fatal("1 failure of 3 must not mark the server unresponsive")
	}
	w.tick(context.Background())
	if !w.Healthy() {
		t.Fatal("2 failures of 3 must not mark the server unresponsive")
	}
	w.tick(context.Background())
	if w.Healthy() {
		t.Fatal("3 consecutive failures should mark the server unresponsive")
	}
}

// A recovered probe has to reset the counter, or an intermittent fault
// accumulates across hours and eventually restarts a working server.
func TestRecoveryResetsFailureCount(t *testing.T) {
	var fail atomic.Bool
	w := testWatchdog(t, 3, func(context.Context) error {
		if fail.Load() {
			return errors.New("timeout")
		}
		return nil
	})

	fail.Store(true)
	w.tick(context.Background())
	w.tick(context.Background())

	fail.Store(false)
	w.tick(context.Background())
	if w.fails.Load() != 0 {
		t.Fatalf("fails = %d after recovery, want 0", w.fails.Load())
	}

	fail.Store(true)
	w.tick(context.Background())
	w.tick(context.Background())
	if !w.Healthy() {
		t.Fatal("the counter did not reset: two failures after a recovery must not be fatal")
	}
}

// Outside systemd there is no watchdog to withhold, so SelfRestart is the only
// way a hang becomes a restart.
func TestSelfRestartExitsWhenNoSystemd(t *testing.T) {
	w := testWatchdog(t, 1, func(context.Context) error { return errors.New("wedged") })
	w.cfg.SelfRestart = true

	var code atomic.Int64
	code.Store(-1)
	w.exit = func(c int) { code.Store(int64(c)) }

	w.tick(context.Background())
	if code.Load() != 1 {
		t.Fatalf("exit code = %d, want 1", code.Load())
	}
}

// Under systemd the restart decision belongs to the init system, including its
// rate limiting, so the process must not exit on its own.
func TestSelfRestartSuppressedUnderSystemd(t *testing.T) {
	w := testWatchdog(t, 1, func(context.Context) error { return errors.New("wedged") })
	w.cfg.SelfRestart = true
	w.notifier = &Notifier{addr: &net.UnixAddr{Name: "/nonexistent/notify", Net: "unixgram"}}

	var exited atomic.Bool
	w.exit = func(int) { exited.Store(true) }

	w.tick(context.Background())
	if exited.Load() {
		t.Fatal("must not exit on its own when systemd owns the restart policy")
	}
	if w.Healthy() {
		t.Fatal("the server should still be marked unresponsive")
	}
}

// Liveness, not correctness: any status proves accept → parse → handler →
// response completed. A 421 for an unconfigured Host is a healthy answer.
func TestHTTPProbeAcceptsAnyStatus(t *testing.T) {
	for _, status := range []int{200, 404, 421, 500, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		addr := srv.Listener.Addr().String()
		probe := httpProbe(addr, false, time.Second)
		if err := probe(context.Background()); err != nil {
			t.Errorf("status %d treated as unhealthy: %v", status, err)
		}
		srv.Close()
	}
}

func TestHTTPProbeFailsWhenNothingIsListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing is listening now

	probe := httpProbe(addr, false, 200*time.Millisecond)
	if err := probe(context.Background()); err == nil {
		t.Fatal("a dead listener must fail the probe")
	}
}

func TestProbeAddr(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{":443", "127.0.0.1:443"},
		{"0.0.0.0:80", "127.0.0.1:80"},
		{"[::]:80", "127.0.0.1:80"},
		{"127.0.0.1:9443", "127.0.0.1:9443"},
		{"10.0.0.5:80", "10.0.0.5:80"},
		{"garbage", ""},
		{"", ""},
	} {
		if got := ProbeAddr(tc.in); got != tc.want {
			t.Errorf("ProbeAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNotifierWithoutSystemd(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	n := NewNotifier()
	if n.Available() {
		t.Fatal("no NOTIFY_SOCKET should mean unavailable")
	}
	if err := n.Send("READY=1"); !errors.Is(err, ErrNoSocket) {
		t.Fatalf("Send err = %v, want ErrNoSocket", err)
	}
	n.Close() // must not panic
}

// A child that inherited the environment must not ping on the main process's
// behalf; systemd only accepts the ping from the PID it is watching.
func TestSystemdIntervalIgnoresForeignPID(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "30000000")
	t.Setenv("WATCHDOG_PID", "1")
	if got := SystemdInterval(); got != 0 {
		t.Fatalf("SystemdInterval() = %v for a foreign PID, want 0", got)
	}

	t.Setenv("WATCHDOG_PID", itoa(os.Getpid()))
	if got := SystemdInterval(); got != 30*time.Second {
		t.Fatalf("SystemdInterval() = %v, want 30s", got)
	}
}

func TestSystemdIntervalUnset(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	t.Setenv("WATCHDOG_PID", "")
	if got := SystemdInterval(); got != 0 {
		t.Fatalf("SystemdInterval() = %v with no WATCHDOG_USEC, want 0", got)
	}
}

func TestDisabledWatchdogRunReturnsImmediately(t *testing.T) {
	w := testWatchdog(t, 1, func(context.Context) error { return errors.New("x") })
	w.cfg.Enabled = false

	done := make(chan struct{})
	go func() { w.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a disabled watchdog must not run")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
