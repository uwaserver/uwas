package siteuser

// F42300/F42301: a key the caller got wrong, an empty key identifier and an
// invalid hostname are input errors (errors.Is ErrInvalidInput); host failures
// are not. A rejected key must be refused before .ssh is created.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSSHKeyInputErrorsAreMarkedAndFailBeforeAnySideEffect(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"
	mkdirCalls := 0
	osMkdirAllFn = func(string, os.FileMode) error { mkdirCalls++; return nil }
	web := filepath.Join(t.TempDir(), "example.com", "public_html")

	for _, key := range []string{"ssh-rsa not-base64!!", "ssh-rsa AAAAB3NzaC1 x", ""} {
		err := AddSSHKeyForWebDir(web, "example.com", key)
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("key %q: err = %v, want ErrInvalidInput", key, err)
		}
	}
	if mkdirCalls != 0 {
		t.Errorf("rejected keys created the .ssh directory %d time(s)", mkdirCalls)
	}
	if err := RemoveSSHKeyForWebDir(web, "example.com", "  "); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("empty identifier: err = %v, want ErrInvalidInput", err)
	}
	if err := AddSSHKeyForWebDir(web, "bad host", testSSHKey1); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bad hostname: err = %v, want ErrInvalidInput", err)
	}
}

// Control: a failure of the host is not an input error, and a valid key does
// reach the directory creation.
func TestSSHKeyHostFailureIsNotAnInputError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"
	osMkdirAllFn = func(string, os.FileMode) error { return fmt.Errorf("disk full") }
	err := AddSSHKeyForWebDir(filepath.Join(t.TempDir(), "example.com", "public_html"), "example.com", testSSHKey1)
	if err == nil || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want a non-input failure", err)
	}
}
