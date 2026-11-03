package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNullUserEntryFailsClosedWithoutPanic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "users.json"), []byte("[null]"), 0600); err != nil {
		t.Fatalf("write users file: %v", err)
	}

	m, err := NewManager(dir, "")
	if m != nil {
		m.Stop()
	}
	if err == nil {
		t.Fatal("NewManager accepted a null users.json entry")
	}
}
