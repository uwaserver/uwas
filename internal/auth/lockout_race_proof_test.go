// Proof-of-bug: RACE-002 brute-force lockout bypassed by concurrent burst.
//
// Bug: internal/auth/manager.go AuthenticateFrom — isLockedOut and
// recordFailedAttempt are not atomic with the bcrypt compare. Between the
// check at line 479 and the record at line 512, the goroutine runs
// bcrypt.CompareHashAndPassword (~1.6s at BcryptCost=14) with no lock held.
// A concurrent burst of N requests all pass isLockedOut before any records
// a failure, admitting N bcrypt compares instead of the intended max=5.
//
// Fix: hold the auth gate (already acquired at line 476) around BOTH the
// isLockedOut check AND recordFailedAttempt, and refactor isLockedOut to
// accept a pre-acquired lock OR do the check-and-update atomically under
// loginAttemptsMu.

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func init() {
	// Force cheap bcrypt so the test runs fast.
	atomic.StoreInt64(&testBcryptCost, 4)
}

// randomHex returns n random hex bytes.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// newTestManagerProof returns a Manager backed by an in-memory store.
func newTestManagerProof(t *testing.T) *Manager {
	m, err := NewManager("", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateUser("attacker", "attacker@test", "correct-password", RoleUser, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return m
}

// TestLockoutBypass_concurrentBurst proves RACE-002: when maxLoginAttempts=5,
// a burst of 5+ concurrent wrong-password logins all bypass the lockout because
// each goroutine passes isLockedOut before any records a failure.
// The bug is in AuthenticateFrom: the auth gate is acquired but isLockedOut
// and recordFailedAttempt both acquire loginAttemptsMu separately, and bcrypt
// runs between them with no lock held.
//
// Proof: after the burst, read loginAttempts count directly.
// - Bug present: all burstSize attempts are recorded (all passed isLockedOut).
// - Bug fixed: at most maxLoginAttempts attempts are recorded (lockout gates early).
func TestLockoutBypass_concurrentBurst(t *testing.T) {
	m := newTestManagerProof(t)
	t.Cleanup(m.Stop)

	const (
		burstSize     = 8                   // more than maxLoginAttempts (5)
		wrongPassword = "wrong-password-x"  // definitely wrong
	)

	// Spawn burstSize concurrent login attempts with a bad password.
	var wg sync.WaitGroup
	results := make([]error, burstSize)
	for i := 0; i < burstSize; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := m.AuthenticateFrom("attacker", wrongPassword, "")
			results[idx] = err
		}(i)
	}
	wg.Wait()

	// Count failures recorded in loginAttempts by reading the map under lock.
	m.loginAttemptsMu.Lock()
	recordedCount := len(m.loginAttempts["attacker"])
	m.loginAttemptsMu.Unlock()

	t.Logf("burstSize=%d recordedAttempts=%d maxLoginAttempts=%d", burstSize, recordedCount, maxLoginAttempts)

	// The bug: all burstSize goroutines passed isLockedOut before any recorded a
	// failure. Each recorded attempt increments loginAttempts after bcrypt returns.
	// With the race, recordedCount == burstSize (all 8 recorded).
	// After fix: recordedCount <= maxLoginAttempts (lockout stops admitting after 5).
	if recordedCount > maxLoginAttempts {
		t.Errorf("\n\nFAIL: brute-force lockout bypassed by concurrent burst\n"+
			"  burstSize=%d requests, loginAttempts recorded=%d (want <= %d)\n"+
			"  All %d goroutines passed isLockedOut before any recorded a failure.\n"+
			"  Root cause: AuthenticateFrom line 479 (isLockedOut) and line 512\n"+
			"  (recordFailedAttempt) are not atomic with bcrypt compare at line 510.\n"+
			"  Fix: hold auth gate around check-and-record OR record failure BEFORE bcrypt.\n",
			burstSize, recordedCount, maxLoginAttempts, burstSize)
	} else {
		t.Logf("PASS: lockout correctly enforced — recordedAttempts=%d <= maxLoginAttempts=%d",
			recordedCount, maxLoginAttempts)
	}
}

// TestLockoutBypass_sequentialBaseline proves the sequential (non-burst) path
// correctly enforces the lockout after maxLoginAttempts failures.
func TestLockoutBypass_sequentialBaseline(t *testing.T) {
	m := newTestManagerProof(t)
	t.Cleanup(m.Stop)

	const wrongPass = "wrong-password-seq"

	// Exhaust the burstSize of the lockout window.
	var admitted, rejected int
	for i := 0; i < 10; i++ {
		_, err := m.AuthenticateFrom("attacker", wrongPass, "")
		if err == nil {
			admitted++
		} else if errors.Is(err, errors.New("too many failed attempts; try again later")) ||
			(err != nil && err.Error() == "too many failed attempts; try again later") {
			rejected++
			break
		}
	}

	if admitted > maxLoginAttempts {
		t.Errorf("sequential: admitted %d > maxLoginAttempts %d", admitted, maxLoginAttempts)
	}
	if rejected == 0 {
		t.Error("sequential: expected at least one lockout rejection")
	}
	t.Logf("sequential baseline: admitted=%d rejected=%d (maxLoginAttempts=%d)",
		admitted, rejected, maxLoginAttempts)
}
