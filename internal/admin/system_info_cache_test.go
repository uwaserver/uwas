//go:build linux

package admin

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func f1720Fake(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func f1720Count(t *testing.T, file string) int {
	t.Helper()
	b, _ := os.ReadFile(file)
	return strings.Count(string(b), "x")
}

// F1721: /system spawned uname, df and timedatectl on every request.
func TestSystemHostFactsCachedAcrossRequests(t *testing.T) {
	bin, cnt := t.TempDir(), t.TempDir()
	f1720Fake(t, bin, "uname", `printf x >> `+cnt+`/uname; echo 6.0.0-fake`)
	f1720Fake(t, bin, "df", `printf x >> `+cnt+`/df; echo "Filesystem 1B-blocks Used Available Use% Mounted"; echo "/dev/x 1000 400 600 40% /"`)
	f1720Fake(t, bin, "timedatectl", `printf x >> `+cnt+`/tz; echo Europe/Istanbul`)
	f1720Fake(t, bin, "apt", `printf x >> `+cnt+`/apt; echo "Listing..."`)
	t.Setenv("PATH", bin+":/usr/bin:/bin")

	s := testServer()
	const n = 20
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		s.handleSystem(rec, httptest.NewRequest("GET", "/api/v1/system", nil))
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		if i == 0 { // let the one-shot background refresh (if any) finish
			deadline := time.Now().Add(5 * time.Second)
			for f1720Count(t, cnt+"/apt") == 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}
	apt := f1720Count(t, cnt+"/apt")
	if apt != 1 {
		t.Fatalf("invalid proof: control failed, apt=%d", apt)
	}
	un, df, tz := f1720Count(t, cnt+"/uname"), f1720Count(t, cnt+"/df"), f1720Count(t, cnt+"/tz")
	if un > 1 || df > 1 || tz > 1 {
		t.Fatalf("host commands spawned per request: uname=%d df=%d timedatectl=%d (F1721)", un, df, tz)
	}

	// Edge: once the host-facts TTL has lapsed the figures refresh exactly once.
	s.sysInfoHostMu.Lock()
	s.sysInfoHostTime = time.Now().Add(-2 * sysInfoHostTTL)
	s.sysInfoHostMu.Unlock()
	for i := 0; i < 3; i++ {
		s.handleSystem(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/system", nil))
	}
	if df2 := f1720Count(t, cnt+"/df"); df2 != df+1 {
		t.Fatalf("df after TTL expiry = %d, want %d", df2, df+1)
	}
	if un2 := f1720Count(t, cnt+"/uname"); un2 != un {
		t.Fatalf("uname re-spawned after TTL expiry: %d, want %d (kernel is cached once)", un2, un)
	}
}

// F1720: the request that wins the cache refresh waited for `apt list` and the
// web-root walk before answering.
func TestSystemFirstRequestDoesNotWaitForApt(t *testing.T) {
	bin, tmp := t.TempDir(), t.TempDir()
	gate := filepath.Join(tmp, "gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	f1720Fake(t, bin, "apt", `cat `+gate+` >/dev/null; echo "Listing..."`)
	t.Setenv("PATH", bin+":/usr/bin:/bin")

	s := testServer()
	done := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		s.handleSystem(rec, httptest.NewRequest("GET", "/api/v1/system", nil))
		close(done)
	}()
	returned := false
	select {
	case <-done:
		returned = true
	case <-time.After(3 * time.Second):
	}
	// Release apt so nothing is left hanging either way.
	if f, err := os.OpenFile(gate, os.O_WRONLY, 0); err == nil {
		f.Close()
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("invalid proof: handler never returned after apt released")
	}
	if !returned {
		t.Fatal("handleSystem waited for apt list / disk walk before answering (F1720)")
	}
}
