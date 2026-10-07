package server

// Regression guard: Start() used to return on an early bind failure without
// joining the signal-handler goroutine it had just added to s.wg (added at
// server.go:938, previously joined only at the wg.Wait on the normal shutdown
// path). handleSignals blocks in select until a signal or ctx cancellation,
// so the leaked goroutine stayed alive forever. joinOnErr now cancels ctx and
// waits on every early-return path; this test pins that the WaitGroup counter
// drains after Start reports a bind failure.

import (
	"net"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestStartEarlyFailureJoinsSignalHandler(t *testing.T) {
	// Hold the port open for the whole test, so the HTTP bind fails
	// deterministically — no reserve/close/rebind window to race.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()

	cfg := &config.Config{}
	cfg.Global.HTTPListen = ln.Addr().String()

	s := New(cfg, logger.New("error", "text"))

	startErr := s.Start()
	if startErr == nil {
		t.Fatal("Start() succeeded on an occupied port; expected a bind failure")
	}

	joined := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(joined)
	}()

	select {
	case <-joined:
		// The counter reached zero: the signal-handler goroutine was joined.
	case <-time.After(10 * time.Second):
		t.Fatal("signal-handler goroutine was not joined after Start returned a bind failure — s.wg never drained (the early-return leak)")
	}
}
