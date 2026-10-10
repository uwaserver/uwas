package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// `uwas serve -c uwas.yaml` names a file explicitly. If it is missing the
// lookup must report that, not fall through to ~/.uwas/uwas.yaml (F1810).
func TestFindConfigExplicitDefaultNameDoesNotFallThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".uwas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".uwas", "uwas.yaml"), []byte("global: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })

	if p, ok := findConfig("uwas.yaml"); ok || p != "uwas.yaml" {
		t.Errorf("explicit missing uwas.yaml: got %q,%v want uwas.yaml,false", p, ok)
	}
	if p, ok := findConfig("other.yaml"); ok || p != "other.yaml" {
		t.Errorf("explicit missing other.yaml: got %q,%v", p, ok)
	}
	// No -c: the search path still finds the user config.
	if _, ok := findConfig(""); !ok {
		t.Error("search without -c should find ~/.uwas/uwas.yaml")
	}
	// An explicit ./uwas.yaml that exists is used as is.
	if err := os.WriteFile("uwas.yaml", []byte("global: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, ok := findConfig("uwas.yaml"); !ok || p != "uwas.yaml" {
		t.Errorf("explicit existing uwas.yaml: got %q,%v", p, ok)
	}
}
