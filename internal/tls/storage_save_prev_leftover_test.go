package uwastls

// F47303: Save keeps the pair it replaces under .prev names while it renames
// the new files in, and removes them afterwards only when both were linked.
// With a key.pem but no cert.pem (an earlier issuance that stopped after the
// key rename), the key was linked and the cert was not, so a finished Save left
// key.pem.prev, an extra copy of a private key (0600) that nothing removes.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLeavesNoPrevFilesAfterSuccess(t *testing.T) {
	save := func(t *testing.T, seed func(dir string)) string {
		t.Helper()
		base := t.TempDir()
		st := NewCertStorage(base)
		dir := filepath.Join(base, "x.test")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		seed(dir)
		c, certPEM, keyPEM := generateTestCertWithExpiry(t, "x.test", 60*24*time.Hour)
		if err := st.Save("x.test", c, keyPEM, certPEM); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	leftovers := func(dir string) []string {
		var out []string
		for _, n := range []string{"key.pem.prev", "cert.pem.prev"} {
			if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
				out = append(out, n)
			}
		}
		return out
	}

	t.Run("key.pem without cert.pem", func(t *testing.T) {
		dir := save(t, func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "key.pem"), []byte("old key"), 0o600); err != nil {
				t.Fatal(err)
			}
		})
		got := leftovers(dir)
		t.Logf("EXPECTED: no .prev files\nACTUAL: %v", got)
		if len(got) != 0 {
			t.Fatalf("PROBLEM CONFIRMED: Save left %v behind", got)
		}
	})
	t.Run("control: fresh directory", func(t *testing.T) {
		if got := leftovers(save(t, func(string) {})); len(got) != 0 {
			t.Errorf("leftovers = %v, want none", got)
		}
	})
	t.Run("control: replacing a complete pair", func(t *testing.T) {
		dir := save(t, func(dir string) {
			for _, n := range []string{"key.pem", "cert.pem"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		})
		if got := leftovers(dir); len(got) != 0 {
			t.Errorf("leftovers = %v, want none", got)
		}
	})
}
