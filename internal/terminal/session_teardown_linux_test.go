//go:build linux

package terminal

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// tdSession runs h.ServeHTTP behind a real listener and closes done when
// ServeHTTP returns, so a test can observe session teardown directly.
type tdSession struct {
	cli  *wsTestClient
	done chan struct{}
	stop func()
}

func tdDial(t *testing.T, h *Handler) *tdSession {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		close(done)
	})}
	go func() { _ = srv.Serve(ln) }()
	addr := ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	req := "GET /terminal HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Origin: http://" + addr + "\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("handshake status %q err %v", status, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	return &tdSession{cli: &wsTestClient{conn: conn, br: br}, done: done, stop: func() { conn.Close(); srv.Close() }}
}

// tdScript writes an executable /bin/sh script and returns its path.
func tdScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "shell.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// tdWaitFile polls (condition wait, not ordering) until path has content.
func tdWaitFile(t *testing.T, path string, bound time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return strings.TrimSpace(string(b))
		}
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("file %s never written", path)
	return ""
}

func tdAlive(pidStr string) bool {
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func tdKill(pidStr string) {
	if pid, err := strconv.Atoi(pidStr); err == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// tdWaitStack polls goroutine stacks until one contains all of subs.
func tdWaitStack(t *testing.T, bound time.Duration, subs ...string) {
	t.Helper()
	deadline := time.Now().Add(bound)
	buf := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			ok := true
			for _, s := range subs {
				if !strings.Contains(g, s) {
					ok = false
					break
				}
			}
			if ok {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no goroutine with %v", subs)
}

// Verifier for F141 (HUP-ignoring shell never torn down) and F142 (blocked
// PTY write hides the disconnect). Every wait is for an event (ServeHTTP
// returning, a file appearing, a goroutine parking); the bounds are failure
// bounds only.

type tdCase struct {
	trapHUP bool // shell ignores SIGHUP
	wedge   bool // foreground program never reads the tty
}

func tdScriptFor(c tdCase) string {
	b := "echo $$ > \"$F14X_PID\"\n"
	if c.trapHUP {
		b += "trap 'echo hup > \"$F14X_MARK\"' HUP\n"
	}
	b += "echo READY\n"
	if c.wedge {
		b += "cat \"$F14X_FIFO\" > /dev/null\nwhile :; do cat \"$F14X_FIFO\" > /dev/null; done\n"
	} else {
		b += "while :; do read -r l; done\n"
	}
	return b
}

type tdRun struct {
	s    *tdSession
	pid  string
	mark string
	fifo string
}

func tdStart(t *testing.T, c tdCase) *tdRun {
	dir := t.TempDir()
	r := &tdRun{mark: filepath.Join(dir, "hup"), fifo: filepath.Join(dir, "fifo")}
	pidf := filepath.Join(dir, "pid")
	if err := syscall.Mkfifo(r.fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("F14X_PID", pidf)
	t.Setenv("F14X_MARK", r.mark)
	t.Setenv("F14X_FIFO", r.fifo)
	r.s = tdDial(t, &Handler{Shell: tdScript(t, tdScriptFor(c))})
	r.pid = tdWaitFile(t, pidf, 10*time.Second)
	t.Cleanup(func() { tdKill(r.pid); r.s.stop() })
	if got := r.s.cli.readTextUntil(t, "READY", 10*time.Second); !strings.Contains(got, "READY") {
		t.Fatalf("no READY, got %q", got)
	}
	return r
}

func (r *tdRun) wedge(t *testing.T, frames int) {
	chunk := bytes.Repeat(append(bytes.Repeat([]byte("a"), 99), '\n'), 600)
	for i := 0; i < 2; i++ {
		if err := r.s.cli.writeFrame(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	tdWaitStack(t, 10*time.Second, "terminal.(*Handler).ServeHTTP", "os.(*File).Write")
	for i := 0; i < frames; i++ { // extra small frames beyond the wedge
		if err := r.s.cli.writeFrame([]byte("x\n")); err != nil {
			t.Fatalf("write extra %d: %v", i, err)
		}
	}
}

func (r *tdRun) expectTeardown(t *testing.T, bound time.Duration) {
	t.Helper()
	select {
	case <-r.s.done:
	case <-time.After(bound):
		t.Fatalf("ServeHTTP did not return within %v (shell alive=%v)", bound, tdAlive(r.pid))
	}
	if tdAlive(r.pid) {
		t.Fatalf("shell pid %s still alive after teardown", r.pid)
	}
}

func tdGrace(t *testing.T, d time.Duration) {
	old := hangupGrace
	hangupGrace = d
	t.Cleanup(func() { hangupGrace = old })
}

// TestServeHTTPTearsDownAbandonedSession pins F141/F142: once the client
// disconnects, the session must end even when the shell ignores SIGHUP or the
// foreground program has stopped reading the PTY with client input pending.
func TestServeHTTPTearsDownAbandonedSession(t *testing.T) {
	t.Run("shell_ignores_sighup", func(t *testing.T) {
		tdGrace(t, 50*time.Millisecond)
		r := tdStart(t, tdCase{trapHUP: true})
		r.s.cli.conn.Close()
		tdWaitFile(t, r.mark, 10*time.Second)
		r.expectTeardown(t, 10*time.Second)
	})
	t.Run("pty_write_blocked", func(t *testing.T) {
		tdGrace(t, time.Hour)
		r := tdStart(t, tdCase{wedge: true})
		r.wedge(t, 3*ptyInputQueue)
		r.s.cli.conn.Close()
		r.expectTeardown(t, 10*time.Second)
	})
	t.Run("both", func(t *testing.T) {
		tdGrace(t, 50*time.Millisecond)
		r := tdStart(t, tdCase{trapHUP: true, wedge: true})
		r.wedge(t, 0)
		r.s.cli.conn.Close()
		r.expectTeardown(t, 10*time.Second)
	})
}
