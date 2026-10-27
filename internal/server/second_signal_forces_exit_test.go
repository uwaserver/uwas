package server

// F31900: handleSignals registered SIGINT/SIGTERM with signal.Notify and never
// released the registration. After the first signal the goroutine returned and
// graceful drain began, but Go kept intercepting the signals, so a second
// Ctrl+C / SIGTERM during the (up to ShutdownGrace) drain was swallowed and the
// operator could not force the process down. The first signal must hand
// SIGINT/SIGTERM back to the default action.

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

const secondSignalChildEnv = "UWAS_SECOND_SIGNAL_CHILD"

// TestSecondSignalChildHelper is the child half of TestSecondShutdownSignalForcesExit.
func TestSecondSignalChildHelper(t *testing.T) {
	if os.Getenv(secondSignalChildEnv) != "1" {
		t.Skip("helper process only")
	}
	cfg := testConfig(t.TempDir())
	cfg.Global.HTTPListen = "127.0.0.1:0"
	s := New(cfg, logger.New("error", "text"))

	// Hold SIGTERM while handleSignals registers so the re-sent signals below
	// cannot kill the child before the handler is listening.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)

	s.wg.Add(1)
	done := make(chan struct{})
	go func() {
		s.handleSignals()
		close(done)
	}()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
wait:
	for {
		select {
		case <-done:
			break wait
		case <-tick.C:
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		case <-time.After(5 * time.Second):
			os.Stdout.WriteString("HANDLER_TIMEOUT\n")
			os.Exit(3)
		}
	}
	// Only handleSignals' own registration may remain from here on.
	signal.Stop(guard)
	os.Stdout.WriteString("DRAINING\n")
	time.Sleep(3 * time.Second)
	os.Stdout.WriteString("SURVIVED\n")
	os.Exit(0)
}

func TestSecondShutdownSignalForcesExit(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSecondSignalChildHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), secondSignalChildEnv+"=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var lines []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		lines = append(lines, l)
		if l == "DRAINING" {
			// Second shutdown signal while the first one's drain is running.
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			break
		}
		if l == "HANDLER_TIMEOUT" {
			break
		}
	}
	for sc.Scan() {
		lines = append(lines, strings.TrimSpace(sc.Text()))
	}
	waitErr := cmd.Wait()
	killed := cmd.ProcessState != nil && cmd.ProcessState.Sys().(syscall.WaitStatus).Signaled()
	t.Logf("EXPECTED: child terminated by the second SIGTERM\nACTUAL: killed=%v err=%v output=%v", killed, waitErr, lines)
	if !killed {
		t.Fatalf("PROBLEM CONFIRMED: second SIGTERM during drain was swallowed (output %v)", lines)
	}
}
