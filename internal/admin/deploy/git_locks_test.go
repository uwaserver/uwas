package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClearStaleGitLocks_RemovesOldShallowLock(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(gitDir, "shallow.lock")
	if err := os.WriteFile(lock, []byte("pid 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Age the lock past the stale threshold.
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}

	n := clearStaleGitLocks(gitDir, staleGitLockAge)
	if n != 1 {
		t.Fatalf("removed=%d want 1", n)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("shallow.lock still present")
	}
}

func TestClearStaleGitLocks_KeepsFreshLock(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(gitDir, "index.lock")
	if err := os.WriteFile(lock, []byte("pid 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	n := clearStaleGitLocks(gitDir, staleGitLockAge)
	if n != 0 {
		t.Fatalf("removed fresh lock: %d", n)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("fresh lock should remain")
	}

	n = clearStaleGitLocks(gitDir, 0)
	if n != 1 {
		t.Fatalf("force remove=%d want 1", n)
	}
}

func TestIsGitLockContention(t *testing.T) {
	err := errors.New("exit status 128")
	out := "fatal: Unable to create '/var/lib/uwas/apps/crm/.git/shallow.lock': File exists.\nAnother git process seems to be running"
	if !isGitLockContention(err, out) {
		t.Fatal("expected lock contention detection")
	}
	if isGitLockContention(err, "fatal: could not read from remote") {
		t.Fatal("network error should not look like lock contention")
	}
}
