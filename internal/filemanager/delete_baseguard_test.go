package filemanager

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: Delete's "cannot delete the web root" guard compared the
// absolute path safePath returns against the raw baseDir argument, so the two
// only matched when the caller already passed an absolute, clean string.
//
// A baseDir with a trailing slash (an operator writing
// `root: /var/www/site/public_html/` — passed through verbatim by
// domainroot.ForDomainWithApps), a relative baseDir, or the empty path the
// admin API sends for an absent ?path= (internal/admin/files/handler.go:405
// passes the query value through with no default, and filepath.Clean("") is
// ".") all slipped past the check, and os.RemoveAll then deleted the entire
// web root instead of rejecting the request.

// newFakeSite builds a fake domain web root with files that must survive a
// rejected base-dir delete.
func newFakeSite(t *testing.T) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	base = filepath.Join(dir, "public_html")
	if err := os.MkdirAll(filepath.Join(base, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(base, "index.php"),
		filepath.Join(base, "sub", "style.css"),
	} {
		if err := os.WriteFile(f, []byte("site content"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, base
}

// assertWebRootIntact fails when the base directory or its contents are gone.
func assertWebRootIntact(t *testing.T, base string) {
	t.Helper()
	if _, err := os.Stat(base); err != nil {
		t.Errorf("web root %q was removed: %v", base, err)
	}
	for _, f := range []string{"index.php", filepath.Join("sub", "style.css")} {
		if _, err := os.Stat(filepath.Join(base, f)); err != nil {
			t.Errorf("web root file %q was removed: %v", f, err)
		}
	}
}

// A base dir with a trailing slash names the same directory, so the guard must
// still hold.
func TestDeleteRejectsBaseDirWithTrailingSlash(t *testing.T) {
	_, base := newFakeSite(t)

	if err := Delete(base+"/", "."); err == nil {
		t.Error("Delete(base+\"/\", \".\") must be rejected, got nil")
	}
	assertWebRootIntact(t, base)
}

// The admin handler forwards an absent ?path= as "", which filepath.Clean
// turns into "." — the base dir itself.
func TestDeleteRejectsEmptyPathAsBaseDir(t *testing.T) {
	_, base := newFakeSite(t)

	if err := Delete(base+"/", ""); err == nil {
		t.Error("Delete(base+\"/\", \"\") must be rejected, got nil")
	}
	assertWebRootIntact(t, base)
}

// A relative baseDir resolves to the same absolute directory; the guard must
// compare resolved forms, not the raw string.
func TestDeleteRejectsBaseDirGivenAsRelativePath(t *testing.T) {
	dir, base := newFakeSite(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Skip("no working directory available")
	}
	if err := os.Chdir(dir); err != nil {
		t.Skipf("cannot chdir into temp dir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	if err := Delete("public_html", "."); err == nil {
		t.Error("Delete(\"public_html\", \".\") must be rejected, got nil")
	}
	assertWebRootIntact(t, base)
}

// A redundant inner path segment resolves to the base dir too.
func TestDeleteRejectsBaseDirViaRedundantSegments(t *testing.T) {
	_, base := newFakeSite(t)

	if err := Delete(base, "./sub/.."); err == nil {
		t.Error("Delete(base, \"./sub/..\") must be rejected, got nil")
	}
	assertWebRootIntact(t, base)
}

// Controls: the guard's existing behaviour and ordinary deletes must survive.
func TestDeleteStillBlocksCanonicalBaseDir(t *testing.T) {
	_, base := newFakeSite(t)

	if err := Delete(base, "."); err == nil {
		t.Error("Delete(base, \".\") must be rejected, got nil")
	}
	assertWebRootIntact(t, base)
}

func TestDeleteStillRemovesOrdinaryFilesAndDirs(t *testing.T) {
	_, base := newFakeSite(t)

	if err := Delete(base, "index.php"); err != nil {
		t.Fatalf("Delete(base, \"index.php\") = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(base, "index.php")); !os.IsNotExist(err) {
		t.Error("index.php should have been deleted")
	}
	if err := Delete(base, "sub"); err != nil {
		t.Fatalf("Delete(base, \"sub\") = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(base, "sub")); !os.IsNotExist(err) {
		t.Error("sub/ should have been deleted")
	}
	// An ordinary delete must never take the web root with it.
	if _, err := os.Stat(base); err != nil {
		t.Errorf("web root should survive ordinary deletes: %v", err)
	}
}

func TestDeleteStillRejectsTraversal(t *testing.T) {
	_, base := newFakeSite(t)

	if err := Delete(base, "../../escape"); err == nil {
		t.Error("traversal path must be rejected, got nil")
	}
}
