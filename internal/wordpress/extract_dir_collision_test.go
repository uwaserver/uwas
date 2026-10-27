package wordpress

// F31203: downloadAndExtract untars into filepath.Dir(webRoot), then moves
// <parent>/wordpress/* into webRoot and RemoveAll's <parent>/wordpress. When the
// document root is itself named "wordpress" (/var/www/wordpress) the extraction
// directory IS the document root: the moves are rename-onto-self no-ops and the
// final RemoveAll deletes the whole web root — the fresh install and anything
// that was already there. A sibling directory called "wordpress" is likewise
// overwritten, drained into this site and removed.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTarCmd emulates `tar xzf <file> -C <dir>` for a WordPress tarball: it
// creates <dir>/wordpress/wp-load.php. Everything else succeeds silently.
func fakeTarCmd(name string, args ...string) *exec.Cmd {
	if name != "tar" {
		return fakeOutputCmd("", "", 0)
	}
	dir := ""
	for i, a := range args {
		if a == "-C" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	return exec.Command("/bin/sh", "-c", `mkdir -p "$1/wordpress" && echo '<?php' > "$1/wordpress/wp-load.php"`, "sh", dir)
}

func extractInto(t *testing.T, webRoot string) error {
	t.Helper()
	snap := saveHooks()
	t.Cleanup(func() { restoreHooks(snap) })
	execCommandFn = fakeTarCmd
	var cleanup func()
	httpGetFn, cleanup = fakeTarHandlerFunc("fake-tar-content")
	t.Cleanup(cleanup)
	osStatFn = os.Stat
	osReadDirFn = os.ReadDir
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	var log strings.Builder
	return downloadAndExtract(webRoot, &log)
}

func TestDownloadAndExtractKeepsWebRootNamedWordpress(t *testing.T) {
	// Control: an ordinary document root.
	base := t.TempDir()
	root := filepath.Join(base, "example.com", "public_html")
	if err := extractInto(t, root); err != nil {
		t.Fatalf("control: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "wp-load.php")); err != nil {
		t.Fatalf("control: wp-load.php missing: %v", err)
	}

	// Defect: the document root is called "wordpress".
	base = t.TempDir()
	root = filepath.Join(base, "wordpress")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(keep, []byte("existing site file"), 0644); err != nil {
		t.Fatal(err)
	}
	err := extractInto(t, root)
	_, loadErr := os.Stat(filepath.Join(root, "wp-load.php"))
	_, keepErr := os.Stat(keep)
	t.Logf("EXPECTED: wp-load.php installed, keep.txt untouched\nACTUAL: err=%v wp-load.php err=%v keep.txt err=%v", err, loadErr, keepErr)
	if err != nil || loadErr != nil || keepErr != nil {
		t.Fatalf("PROBLEM CONFIRMED: extraction into a web root named \"wordpress\" destroyed it")
	}
}

func TestDownloadAndExtractLeavesSiblingWordpressDirAlone(t *testing.T) {
	// The extraction used to land in <parent>/wordpress, a name another site
	// may own. Its files must neither be overwritten nor moved into this root.
	parent := t.TempDir()
	sibling := filepath.Join(parent, "wordpress")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(sibling, "other-site.php")
	if err := os.WriteFile(other, []byte("other tenant"), 0644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "mysite")
	if err := extractInto(t, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("PROBLEM CONFIRMED: sibling site's file was moved away: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "other-site.php")); err == nil {
		t.Errorf("PROBLEM CONFIRMED: sibling site's file was moved into this web root")
	}
	if _, err := os.Stat(filepath.Join(root, "wp-load.php")); err != nil {
		t.Errorf("wp-load.php missing from web root: %v", err)
	}
}
