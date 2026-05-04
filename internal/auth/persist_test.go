package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJWTSecret_PersistsAcrossManagerReload(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "")
	first := append([]byte(nil), m1.jwtSecret...)
	if len(first) < 32 {
		t.Fatalf("first secret too short: %d", len(first))
	}

	// File should exist with mode 0600 on POSIX (skipped on Windows).
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); err != nil {
		t.Fatalf("auth.json not created: %v", err)
	}

	m2 := NewManager(dir, "")
	if !bytes.Equal(first, m2.jwtSecret) {
		t.Fatalf("jwt secret rotated across reload: was %x now %x", first[:4], m2.jwtSecret[:4])
	}
}

func TestSessions_PersistAcrossManagerReload(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "")
	if _, err := m1.CreateUser("alice", "alice@example.com", "secret123", RoleAdmin, nil); err != nil {
		t.Fatalf("create user: %v", err)
	}
	sess, err := m1.Authenticate("alice", "secret123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "sessions.json")); err != nil {
		t.Fatalf("sessions.json not written: %v", err)
	}

	// Reload and ensure the session is still valid.
	m2 := NewManager(dir, "")
	got, err := m2.ValidateSession(sess.Token)
	if err != nil {
		t.Fatalf("session lost across reload: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("expected alice, got %q", got.Username)
	}

	// Logout removes the session and that survives a reload too.
	m2.Logout(sess.Token)
	m3 := NewManager(dir, "")
	if _, err := m3.ValidateSession(sess.Token); err == nil {
		t.Errorf("expected session invalid after logout+reload")
	}
}

func TestSessions_ExpiredAreNotReloaded(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "")
	if _, err := m1.CreateUser("bob", "bob@example.com", "secret123", RoleAdmin, nil); err != nil {
		t.Fatalf("create user: %v", err)
	}
	sess, err := m1.Authenticate("bob", "secret123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Force expiry and persist.
	m1.mu.Lock()
	m1.sessions[sess.Token].ExpiresAt = m1.sessions[sess.Token].CreatedAt.Add(-1)
	m1.mu.Unlock()
	m1.saveSessions()

	m2 := NewManager(dir, "")
	if _, err := m2.ValidateSession(sess.Token); err == nil {
		t.Errorf("expired session should not be reloaded")
	}
}

func TestSessionCleanupLoop_PrunesExpiredFromDisk(t *testing.T) {
	dir := t.TempDir()

	m := NewManager(dir, "")
	defer m.Stop()

	if _, err := m.CreateUser("carol", "c@example.com", "secret123", RoleAdmin, nil); err != nil {
		t.Fatalf("create user: %v", err)
	}
	sess, err := m.Authenticate("carol", "secret123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Force-expire the session and trigger cleanup directly (don't wait an hour).
	m.mu.Lock()
	m.sessions[sess.Token].ExpiresAt = time.Now().Add(-1 * time.Minute)
	m.mu.Unlock()
	m.CleanupSessions()

	// In memory: gone.
	m.mu.RLock()
	_, stillThere := m.sessions[sess.Token]
	m.mu.RUnlock()
	if stillThere {
		t.Errorf("expired session not removed from memory")
	}

	// On disk: gone too — reload a fresh manager and confirm.
	m2 := NewManager(dir, "")
	defer m2.Stop()
	if _, err := m2.ValidateSession(sess.Token); err == nil {
		t.Errorf("expired session resurrected via sessions.json reload — cleanup did not persist")
	}
}

func TestStop_IsIdempotent(t *testing.T) {
	m := NewManager(t.TempDir(), "")
	m.Stop()
	m.Stop() // must not panic
	m.Stop()
}

func TestJWTSecret_NoDataDirGeneratesEphemeral(t *testing.T) {
	m := NewManager("", "")
	if len(m.jwtSecret) < 32 {
		t.Fatalf("expected ephemeral secret of >= 32 bytes, got %d", len(m.jwtSecret))
	}
	// Second call with no dataDir gets a different (random) secret — that's the
	// expected "no persistence" behaviour, not a bug.
	m2 := NewManager("", "")
	if bytes.Equal(m.jwtSecret, m2.jwtSecret) {
		t.Errorf("two no-dataDir managers should not share a secret")
	}
}
