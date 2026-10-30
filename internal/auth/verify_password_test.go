package auth

// F39100: VerifyPassword is the "prove the current password" check behind
// replacing your own API key. It must accept only the right password, refuse
// an unknown user, and share ChangePassword's failure bucket so the two cannot
// be alternated to multiply guesses.

import (
	"strings"
	"testing"
)

func TestVerifyPassword(t *testing.T) {
	m, _ := newChangePasswordManager(t, 4)

	if err := m.VerifyPassword("alice", "correct horse battery"); err != nil {
		t.Fatalf("right password: %v", err)
	}
	if err := m.VerifyPassword("alice", "wrong"); err == nil || !strings.Contains(err.Error(), "invalid current password") {
		t.Fatalf("wrong password: err=%v, want invalid current password", err)
	}
	if err := m.VerifyPassword("nobody", "x"); err == nil || !strings.Contains(err.Error(), "user not found") {
		t.Fatalf("unknown user: err=%v, want user not found", err)
	}
}

func TestVerifyPasswordSharesChangePasswordThrottle(t *testing.T) {
	m, _ := newChangePasswordManager(t, 4)

	// Wrong guesses through ChangePassword must lock VerifyPassword out too.
	locked := false
	for i := 0; i < 50 && !locked; i++ {
		err := m.ChangePassword("alice", "wrong", "irrelevant-new-pass")
		locked = err != nil && strings.Contains(err.Error(), "too many failed attempts")
	}
	if !locked {
		t.Fatal("ChangePassword never throttled; precondition of the test")
	}
	if err := m.VerifyPassword("alice", "correct horse battery"); err == nil || !strings.Contains(err.Error(), "too many failed attempts") {
		t.Fatalf("VerifyPassword during lockout: err=%v, want too many failed attempts", err)
	}
}
