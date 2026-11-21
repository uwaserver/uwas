package acme

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// F64900: only a missing account.key means "register a new account". Any other
// read error must be returned, or a fresh key is renamed over the only copy of
// the registered one.
func TestLoadOrCreateAccountKeyReadErrorKeepsKey(t *testing.T) {
	log := logger.New("error", "text")
	seed := NewClient("https://example.invalid/dir", t.TempDir(), log)
	if err := seed.loadOrCreateAccountKey(); err != nil {
		t.Fatal(err)
	}
	orig := encodeECKey(seed.accountKey)

	dir := t.TempDir()
	p := filepath.Join(dir, "account.key")
	if err := os.WriteFile(p, orig, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0600) })
	if _, err := os.ReadFile(p); err == nil {
		t.Skip("a 0000 file is readable here (running as root)")
	}

	c := NewClient("https://example.invalid/dir", dir, log)
	if err := c.loadOrCreateAccountKey(); err == nil {
		t.Fatal("unreadable account.key was treated as missing")
	}
	if c.accountKey != nil {
		t.Fatal("an account key was set despite the read error")
	}
	os.Chmod(p, 0600)
	if after, _ := os.ReadFile(p); !bytes.Equal(after, orig) {
		t.Fatal("the existing account.key was overwritten")
	}

	c2 := NewClient("https://example.invalid/dir", dir, log)
	if err := c2.loadOrCreateAccountKey(); err != nil || !c2.accountKey.Equal(seed.accountKey) {
		t.Fatalf("readable key not reused: %v", err)
	}
}
