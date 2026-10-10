package admin

// Regression test for F820: admin Close stops every cloudflared connector (single, several,
// none, one in crash-restart backoff, and StopAll racing Stop).

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// closeTunnelConnector installs a fake cloudflared that records its pid and then
// blocks reading a FIFO the test holds open, so it lives until killed.
func closeTunnelConnector(t *testing.T) (pidDir string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	pidDir = filepath.Join(dir, "pids")
	fifo := filepath.Join(dir, "hold.fifo")
	for _, d := range []string{bin, pidDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	hold, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hold.Close() })
	script := "#!/bin/sh\necho $$ > \"$CLOSETUNNEL_PIDDIR/$TUNNEL_TOKEN.pid\"\nexec cat \"$CLOSETUNNEL_FIFO\"\n"
	if err := os.WriteFile(filepath.Join(bin, "cloudflared"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("CLOSETUNNEL_PIDDIR", pidDir)
	t.Setenv("CLOSETUNNEL_FIFO", fifo)
	return pidDir
}

func closeTunnelWaitPID(t *testing.T, pidDir, token string) int {
	t.Helper()
	p := filepath.Join(pidDir, token+".pid")
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); runtime.Gosched() {
		b, err := os.ReadFile(p)
		if err == nil && strings.HasSuffix(string(b), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil {
				return pid
			}
		}
	}
	t.Fatalf("fake cloudflared %s never started", token)
	return 0
}

// closeTunnelAlive reports whether pid is a live (non-zombie) process.
func closeTunnelAlive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}

// closeTunnelAliveAfter waits (bounded, no sleep) for pid to die; returns true if it
// is still alive when the bound expires.
func closeTunnelAliveAfter(pid int, bound time.Duration) bool {
	for deadline := time.Now().Add(bound); time.Now().Before(deadline); runtime.Gosched() {
		if !closeTunnelAlive(pid) {
			return false
		}
	}
	return closeTunnelAlive(pid)
}

func TestAdminCloseStopsTunnelConnectors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses /proc and a FIFO-held fake cloudflared")
	}
	pidDir := closeTunnelConnector(t)
	ok, bad := 0, 0
	expect := func(label string, cond bool) {
		if cond {
			ok++
			fmt.Printf("OK   %s\n", label)
		} else {
			bad++
			fmt.Printf("MISMATCH %s\n", label)
		}
	}

	// 1. Repro, twice: Close kills a running connector.
	for i := 0; i < 2; i++ {
		s := testServer()
		tok := fmt.Sprintf("rep%d", i)
		if err := s.cfRunner.Start("t-"+tok, tok); err != nil {
			t.Fatal(err)
		}
		pid := closeTunnelWaitPID(t, pidDir, tok)
		s.Close()
		alive := closeTunnelAliveAfter(pid, 10*time.Second)
		expect(fmt.Sprintf("repro %d: connector dead after Close", i), !alive)
		for deadline := time.Now().Add(10 * time.Second); s.cfRunner.IsRunning("t-"+tok) && time.Now().Before(deadline); runtime.Gosched() {
		}
		expect(fmt.Sprintf("repro %d: runner reports not running", i), !s.cfRunner.IsRunning("t-"+tok))
		if alive {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}

	// 2. Several connectors: all stopped.
	{
		s := testServer()
		var pids []int
		for i := 0; i < 3; i++ {
			tok := fmt.Sprintf("multi%d", i)
			if err := s.cfRunner.Start("t-"+tok, tok); err != nil {
				t.Fatal(err)
			}
			pids = append(pids, closeTunnelWaitPID(t, pidDir, tok))
		}
		s.Close()
		dead := 0
		for _, pid := range pids {
			if !closeTunnelAliveAfter(pid, 10*time.Second) {
				dead++
			} else {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		expect(fmt.Sprintf("3 connectors: %d/3 dead after Close", dead), dead == 3)
	}

	// 3. No tunnels: Close is a no-op for the runner (no panic).
	{
		s := testServer()
		s.Close()
		expect("Close with no tunnels: no panic", true)
	}

	// 4. Connector in restart backoff (crashed): Close must prevent the respawn.
	{
		s := testServer()
		tok := "backoff"
		if err := s.cfRunner.Start("t-"+tok, tok); err != nil {
			t.Fatal(err)
		}
		pid := closeTunnelWaitPID(t, pidDir, tok)
		pidFile := filepath.Join(pidDir, tok+".pid")
		_ = os.Remove(pidFile)
		_ = syscall.Kill(pid, syscall.SIGKILL)
		// Gate: monitor has observed the exit (cmd cleared) and is in backoff.
		for deadline := time.Now().Add(10 * time.Second); s.cfRunner.IsRunning("t-"+tok) && time.Now().Before(deadline); runtime.Gosched() {
		}
		expect("backoff: crash observed before Close", !s.cfRunner.IsRunning("t-"+tok))
		s.Close()
		respawned := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); runtime.Gosched() {
			if _, err := os.Stat(pidFile); err == nil {
				respawned = true
				break
			}
		}
		expect("backoff: no respawn after Close (waited past 2s backoff)", !respawned)
		if respawned {
			_ = s.cfRunner.Stop("t-" + tok)
		}
	}

	// 5. StopAll racing explicit Stop on the same tunnels (released together).
	{
		s := testServer()
		var pids []int
		for i := 0; i < 4; i++ {
			tok := fmt.Sprintf("race%d", i)
			if err := s.cfRunner.Start("t-"+tok, tok); err != nil {
				t.Fatal(err)
			}
			pids = append(pids, closeTunnelWaitPID(t, pidDir, tok))
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if i%2 == 0 {
					_ = s.cfRunner.StopAll()
				} else {
					_ = s.cfRunner.Stop(fmt.Sprintf("t-race%d", i%4))
				}
			}(i)
		}
		close(start)
		wg.Wait()
		s.Close()
		dead := 0
		for _, pid := range pids {
			if !closeTunnelAliveAfter(pid, 10*time.Second) {
				dead++
			} else {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		expect(fmt.Sprintf("16 concurrent StopAll/Stop: %d/4 dead", dead), dead == 4)
	}

	fmt.Printf("OK=%d MISMATCH=%d\n", ok, bad)
	if bad > 0 {
		t.Fatalf("FIX NOT VERIFIED")
	}
	fmt.Println("FIX VERIFIED")
}
