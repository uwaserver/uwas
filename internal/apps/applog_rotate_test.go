package apps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F57002: app and build logs were append-only and never rotated.
func TestOpenAppLogRotatesOversizedLog(t *testing.T) {
	old := appLogMaxBytes
	t.Cleanup(func() { appLogMaxBytes = old })
	appLogMaxBytes = 100

	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 500)), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openAppLog(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("new\n")
	f.Close()

	if st, _ := os.Stat(path); st == nil || st.Size() != 4 {
		t.Fatalf("live log size = %v, want only the new 4 bytes", st)
	}
	if st, err := os.Stat(path + ".1"); err != nil || st.Size() != 500 {
		t.Fatalf("rotated log = %v %v, want the old 500 bytes", st, err)
	}
	if st, _ := os.Stat(path + ".1"); st != nil && st.Mode().Perm() != 0o600 {
		t.Errorf("rotated log mode = %v, want 0600", st.Mode().Perm())
	}
}

// Control: a log under the cap keeps appending and is not rotated.
func TestOpenAppLogKeepsSmallLog(t *testing.T) {
	old := appLogMaxBytes
	t.Cleanup(func() { appLogMaxBytes = old })
	appLogMaxBytes = 100

	path := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(path, []byte("old\n"), 0o600)
	f, err := openAppLog(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("new\n")
	f.Close()
	b, _ := os.ReadFile(path)
	if string(b) != "old\nnew\n" {
		t.Fatalf("log = %q", b)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("small log was rotated")
	}
}
