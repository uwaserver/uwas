//go:build linux

package auth

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func piGoroutine(marker string) string {
	buf := make([]byte, 1<<20)
	all := string(buf[:runtime.Stack(buf, true)])
	for _, g := range strings.Split(all, "\n\n") {
		if strings.Contains(g, marker) {
			return g
		}
	}
	return ""
}

func piWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second) // failure bound, not ordering
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

// TestRevokedSessionNotResurrectedByOlderWrite pins F166: a sessions.json
// write carrying a snapshot taken before a revocation must not commit after
// the revoking write. The older writer is parked on a FIFO temp file.
func TestRevokedSessionNotResurrectedByOlderWrite(t *testing.T) {
	for _, tc := range []struct {
		name, marker string
		revoke       func(m *Manager, tok string) error
	}{
		{"change-password", "ChangePassword", func(m *Manager, _ string) error {
			return m.ChangePassword("victim", "old-pass-1", "new-pass-2")
		}},
		{"logout", "Logout", func(m *Manager, tok string) error { m.Logout(tok); return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			m, err := NewManager(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Stop()
			m.CreateUser("victim", "", "old-pass-1", RoleUser, nil)
			m.CreateUser("other", "", "other-pass-1", RoleUser, nil)
			vs, err := m.Authenticate("victim", "old-pass-1")
			if err != nil {
				t.Fatal(err)
			}
			tmp := filepath.Join(dir, "sessions.json.tmp")
			gate := filepath.Join(dir, "gate")
			if err := syscall.Mkfifo(tmp, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(tmp, gate); err != nil {
				t.Fatal(err)
			}
			aDone := make(chan error, 1)
			go func() { _, err := m.Authenticate("other", "other-pass-1"); aDone <- err }()
			piWait(t, "older writer parked in writeSessions", func() bool {
				g := piGoroutine("auth.(*Manager).AuthenticateFrom")
				return strings.Contains(g, "auth.(*Manager).writeSessions") && strings.Contains(g, "os.OpenFile")
			})
			os.Remove(tmp)

			rDone := make(chan error, 1)
			go func() { rDone <- tc.revoke(m, vs.Token) }()
			piWait(t, "revoking writer queued behind the older write", func() bool {
				select {
				case <-rDone:
					t.Fatal("revoking write committed while an older snapshot write was in flight")
				default:
				}
				g := piGoroutine("auth.(*Manager)." + tc.marker)
				return strings.Contains(g, "auth.(*Manager).saveSessions") && strings.Contains(g, "sync.runtime_SemacquireMutex")
			})

			r, err := os.Open(gate)
			if err != nil {
				t.Fatal(err)
			}
			io.ReadAll(r)
			r.Close()
			if err := <-aDone; err != nil {
				t.Fatalf("other login: %v", err)
			}
			if err := <-rDone; err != nil {
				t.Fatalf("revoke: %v", err)
			}
			m2, err := NewManager(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			defer m2.Stop()
			if _, err := m2.ValidateSession(vs.Token); err == nil {
				t.Fatal("revoked session valid after restart")
			}
		})
	}
}

// TestFailedUsersSaveKeepsPreviousFile pins F167: a users.json save that
// fails part-way (EFBIG via RLIMIT_FSIZE) leaves the previous file intact.
func TestFailedUsersSaveKeepsPreviousFile(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	if _, err := m.CreateUser("alice", "", "alice-pass-1", RoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "users.json")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatal(err)
	}
	lim := old
	lim.Cur = uint64(st.Size()) / 2
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
		t.Fatal(err)
	}
	_, createErr := m.CreateUser("bob", "", "bob-pass-1", RoleUser, nil)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatal(err)
	}
	if createErr == nil {
		t.Fatal("expected persist error from the injected write failure")
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var users []*User
	if err := json.Unmarshal(data, &users); err != nil {
		t.Fatalf("users.json torn by failed save: %v", err)
	}
	if len(users) != 1 || users[0].Username != "alice" {
		t.Fatalf("users.json = %d users, want alice only", len(users))
	}
}

// TestCorruptUsersFileFailsClosed pins F168: a corrupt users.json must not
// load as an empty user table (which reopens first-admin bootstrap).
func TestCorruptUsersFileFailsClosed(t *testing.T) {
	for name, content := range map[string]string{
		"empty":     "",
		"truncated": `[{"id":"1","username":"alice","role":"admin"`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "users.json"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			m, err := NewManager(dir, "")
			if err == nil {
				defer m.Stop()
				if _, berr := m.CreateFirstAdmin("mallory", "", "mallory-pass-1"); berr == nil {
					t.Fatal("corrupt users.json reopened first-admin bootstrap")
				}
				t.Fatal("NewManager accepted corrupt users.json")
			}
		})
	}
}
