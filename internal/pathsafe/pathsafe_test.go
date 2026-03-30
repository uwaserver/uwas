package pathsafe

import (
	"path/filepath"
	"testing"
)

func TestIsWithinBase(t *testing.T) {
	base := filepath.Join("var", "www")
	if !IsWithinBase(base, filepath.Join(base, "site", "index.html")) {
		t.Fatal("expected child path to be inside base")
	}
}

func TestIsWithinBaseRejectsPrefixSibling(t *testing.T) {
	base := filepath.Join("var", "www")
	sibling := filepath.Join("var", "www2", "secret.txt")
	if IsWithinBase(base, sibling) {
		t.Fatal("prefix sibling must not be considered inside base")
	}
}

func TestIsWithinBaseResolvedWithMissingTail(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "missing", "file.txt")
	if !IsWithinBaseResolved(root, target) {
		t.Fatal("expected missing-tail path under root to be accepted")
	}
}
