package auth

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newChangePasswordManager(t *testing.T, cost int64) (*Manager, string) {
	t.Helper()
	orig := atomic.LoadInt64(&testBcryptCost)
	atomic.StoreInt64(&testBcryptCost, cost)
	t.Cleanup(func() { atomic.StoreInt64(&testBcryptCost, orig) })
	m, err := NewManager("", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	if _, err := m.CreateUser("alice", "", "correct horse battery", RoleUser, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := m.Authenticate("alice", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	return m, sess.Token
}

// ChangePassword used to run both bcrypt operations under the manager-wide
// write lock, so a single wrong-password attempt stalled every ValidateSession
// (F1871). The baseline for "stalled" is the duration of the bcrypt compare.
func TestChangePasswordDoesNotHoldManagerLockDuringBcrypt(t *testing.T) {
	m, token := newChangePasswordManager(t, 10)

	var worst time.Duration
	var mu sync.Mutex
	stop := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			t0 := time.Now()
			m.ValidateSession(token)
			d := time.Since(t0)
			mu.Lock()
			if d > worst {
				worst = d
			}
			mu.Unlock()
			once.Do(func() { close(started) })
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	<-started

	t0 := time.Now()
	if err := m.ChangePassword("alice", "wrong guess", "another long password"); err == nil {
		t.Error("wrong current password accepted")
	}
	took := time.Since(t0)
	close(stop)
	wg.Wait()

	if worst >= took/2 {
		t.Errorf("ValidateSession stalled %v while ChangePassword's bcrypt compare took %v", worst, took)
	}
}

// Wrong current passwords are throttled like failed logins (F1872): a stolen
// session token must not be usable as an unlimited password-guessing oracle.
func TestChangePasswordThrottlesWrongCurrentPassword(t *testing.T) {
	m, _ := newChangePasswordManager(t, 4)

	for i := 0; i < maxLoginAttempts; i++ {
		if err := m.ChangePassword("alice", "guess", "another long password"); err == nil ||
			!strings.Contains(err.Error(), "invalid current password") {
			t.Fatalf("guess %d: err=%v, want invalid current password", i, err)
		}
	}
	// Locked: even the right password is refused.
	if err := m.ChangePassword("alice", "correct horse battery", "another long password"); err == nil ||
		!strings.Contains(err.Error(), "too many failed attempts") {
		t.Fatalf("after %d wrong guesses: err=%v, want lockout", maxLoginAttempts, err)
	}
	// The lockout is scoped to password changes: login is unaffected.
	if _, err := m.AuthenticateFrom("alice", "correct horse battery", "9.9.9.9"); err != nil {
		t.Errorf("login after change-password lockout: %v", err)
	}
}

func TestChangePasswordEdgeCases(t *testing.T) {
	t.Run("success revokes sessions and clears failures", func(t *testing.T) {
		m, token := newChangePasswordManager(t, 4)
		for i := 0; i < maxLoginAttempts-1; i++ {
			m.ChangePassword("alice", "guess", "another long password")
		}
		if err := m.ChangePassword("alice", "correct horse battery", "another long password"); err != nil {
			t.Fatalf("change: %v", err)
		}
		if _, err := m.ValidateSession(token); err == nil {
			t.Error("old session still valid after password change")
		}
		if _, err := m.Authenticate("alice", "another long password"); err != nil {
			t.Errorf("login with new password: %v", err)
		}
		// Failures were cleared: a fresh round of wrong guesses is not locked yet.
		if err := m.ChangePassword("alice", "guess", "x"); err == nil || strings.Contains(err.Error(), "too many") {
			t.Errorf("failures not cleared by success: %v", err)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		m, _ := newChangePasswordManager(t, 4)
		if err := m.ChangePassword("nobody", "a", "b"); err == nil || !strings.Contains(err.Error(), "user not found") {
			t.Errorf("err=%v, want user not found", err)
		}
	})

	t.Run("two concurrent changes against the same old password: exactly one wins", func(t *testing.T) {
		m, _ := newChangePasswordManager(t, 4)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var wins atomic.Int32
		for _, np := range []string{"first new password!", "second new password"} {
			wg.Add(1)
			go func(np string) {
				defer wg.Done()
				<-start
				if m.ChangePassword("alice", "correct horse battery", np) == nil {
					wins.Add(1)
				}
			}(np)
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 {
			t.Errorf("successful changes = %d, want 1", wins.Load())
		}
	})
}
