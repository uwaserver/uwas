package admin

import (
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

// Usernames are 3-32 characters, but handleLogin accepts a JSON body of up to
// 1 MiB, so a public request could name itself with ~1 MiB of text. That text
// was stored as a key of the per-user failure limiter (and of the auth
// manager's lockout table) for the whole window: memory an unauthenticated
// client can pin, one request at a time. A name no account can have is now
// refused before it reaches either table.
func TestLoginRefusesOversizedUsernameBeforeStoringIt(t *testing.T) {
	s := testServer()
	dir := t.TempDir()
	seedAuthUsers(t, dir, nil, "alice")
	mgr, err := auth.NewManager(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	mgr.SetAuditRecorder(s.RecordAuditR)
	s.SetAuthManager(mgr)

	huge := strings.Repeat("a", 400_000)
	if code, p := postLogin(s, huge, "guess"); code != 401 {
		t.Fatalf("oversized username: code=%d panic=%q, want 401", code, p)
	}

	s.rlMu.Lock()
	keyBytes := 0
	for k := range s.userRateLimits {
		keyBytes += len(k)
	}
	for k := range s.rateLimit {
		keyBytes += len(k)
	}
	s.rlMu.Unlock()
	t.Logf("EXPECTED: well under 4 KiB of limiter keys\nACTUAL: %d bytes of limiter keys", keyBytes)
	if keyBytes > 4096 {
		t.Fatalf("PROBLEM CONFIRMED: %d bytes of request-controlled text stored as limiter keys", keyBytes)
	}

	// The refusal still counts against the client's IP, so oversized names
	// cannot be sprayed for free.
	for i := 0; i < 15; i++ {
		postLogin(s, huge, "guess")
	}
	if !s.checkRateLimit("10.9.8.7", "") {
		t.Error("IP not blocked after repeated oversized-username logins")
	}
}

// Control: an ordinary wrong password for a real account and for an unknown
// name are answered and recorded exactly as before.
func TestLoginOrdinaryFailuresStillRecorded(t *testing.T) {
	s := testServer()
	dir := t.TempDir()
	seedAuthUsers(t, dir, nil, "alice")
	mgr, err := auth.NewManager(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	mgr.SetAuditRecorder(s.RecordAuditR)
	s.SetAuthManager(mgr)

	for _, user := range []string{"alice", "nosuchuser", strings.Repeat("b", 32)} {
		if code, p := postLogin(s, user, "guess"); code != 401 {
			t.Errorf("%q: code=%d panic=%q, want 401", user, code, p)
		}
	}
	s.rlMu.Lock()
	_, aliceCounted := s.userRateLimits["alice"]
	_, maxCounted := s.userRateLimits[strings.Repeat("b", 32)]
	s.rlMu.Unlock()
	if !aliceCounted || !maxCounted {
		t.Errorf("failures of ordinary names must be counted: alice=%v 32-char=%v", aliceCounted, maxCounted)
	}
	if code, p := postLogin(s, "alice", "correct horse battery"); code != 200 {
		t.Errorf("a valid login must still work: code=%d panic=%q", code, p)
	}
}
