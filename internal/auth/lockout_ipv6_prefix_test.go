package auth

// F38000: the per-(username, IP) login lockout keyed an IPv6 client by its full
// address. A subscriber owns at least a /64, so rotating the low 64 bits per
// attempt never reached the threshold and the per-source cap on password
// guessing did not apply. The HTTP-level failure limiter already counts v6
// clients by /64 (F10700); the manager's lockout must agree.

import (
	"fmt"
	"strings"
	"testing"
)

func lockoutProbe(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateUser("victim", "v@test", "S3cure-Passw0rd!", RoleUser, nil); err != nil {
		t.Fatal(err)
	}
	return m
}

func lockedOut(err error) bool {
	return err != nil && strings.Contains(err.Error(), "too many failed attempts")
}

func TestLockoutCountsIPv6ClientByPrefix(t *testing.T) {
	m := lockoutProbe(t)
	// Eight wrong guesses, each from a different address inside one /64.
	var last error
	for i := 0; i < 8; i++ {
		ip := fmt.Sprintf("2001:db8:1:2::%x", i+1)
		_, last = m.AuthenticateFrom("victim", "wrong-password", ip)
	}
	t.Logf("EXPECTED: lockout after %d failures from one /64\nACTUAL: last error = %v", maxLoginAttempts, last)
	if !lockedOut(last) {
		t.Fatalf("PROBLEM CONFIRMED: rotating the low 64 bits evaded the lockout (last error %v)", last)
	}
	// The right password from yet another address of the same /64 is refused too.
	if _, err := m.AuthenticateFrom("victim", "S3cure-Passw0rd!", "2001:db8:1:2::ffff"); !lockedOut(err) {
		t.Fatalf("correct password from the locked /64 = %v, want lockout", err)
	}
}

func TestLockoutIPv6ControlsStayScoped(t *testing.T) {
	m := lockoutProbe(t)
	for i := 0; i < maxLoginAttempts; i++ {
		_, _ = m.AuthenticateFrom("victim", "wrong-password", "2001:db8:1:2::1")
	}
	// A different /64, a v4 client and an IPv4-mapped spelling of another v4
	// client are not affected by the locked /64.
	for _, ip := range []string{"2001:db8:9:9::1", "203.0.113.7", "::ffff:198.51.100.4"} {
		if _, err := m.AuthenticateFrom("victim", "S3cure-Passw0rd!", ip); err != nil {
			t.Errorf("login from %s = %v, want success", ip, err)
		}
	}
}

func TestLockoutIPv4SpellingsShareOneBucket(t *testing.T) {
	m := lockoutProbe(t)
	spellings := []string{"198.51.100.4", "::ffff:198.51.100.4", "198.51.100.4", "::ffff:198.51.100.4", "198.51.100.4"}
	for _, ip := range spellings {
		_, _ = m.AuthenticateFrom("victim", "wrong-password", ip)
	}
	if _, err := m.AuthenticateFrom("victim", "S3cure-Passw0rd!", "198.51.100.4"); !lockedOut(err) {
		t.Fatalf("IPv4 and IPv4-mapped spellings counted separately: %v", err)
	}
}

func TestLockoutKeyNormalization(t *testing.T) {
	cases := map[string]string{
		"":                       "victim",
		"203.0.113.7":            "victim|203.0.113.7",
		"::ffff:203.0.113.7":     "victim|203.0.113.7",
		"2001:db8:1:2:aa:bb:c:d": "victim|2001:db8:1:2::/64",
		"fe80::1%eth0":           "victim|fe80::/64",
		"not-an-ip":              "victim|not-an-ip",
	}
	for in, want := range cases {
		if got := lockoutKey("victim", in); got != want {
			t.Errorf("lockoutKey(victim, %q) = %q, want %q", in, got, want)
		}
	}
}
