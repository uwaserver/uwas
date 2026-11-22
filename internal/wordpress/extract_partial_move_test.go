package wordpress

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// partialMoveTar emulates `tar xzf … -C <dir>` for an archive holding
// wordpress/{index.php, wp-admin/, wp-content/, wp-includes/}.
func partialMoveTar(name string, args ...string) *exec.Cmd {
	if name != "tar" {
		return fakeOutputCmd("", "", 0)
	}
	dir := ""
	for i, a := range args {
		if a == "-C" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	return exec.Command("/bin/sh", "-c", `w="$1/wordpress"; mkdir -p "$w/wp-admin" "$w/wp-content" "$w/wp-includes" &&
echo WP > "$w/index.php" && echo A > "$w/wp-admin/admin.php" && echo C > "$w/wp-content/index.php" && echo V > "$w/wp-includes/version.php"`, "sh", dir)
}

// TestExtractConflictChangesNothing: when wordpress/* cannot all be moved into
// the web root (a wp-content the tenant uploaded first), the install fails
// without having replaced index.php or moved wp-admin in, so the root is as it
// was and a retry after moving the conflict aside succeeds (F68601).
func TestExtractConflictChangesNothing(t *testing.T) {
	snap := saveHooks()
	t.Cleanup(func() { restoreHooks(snap) })
	execCommandFn = partialMoveTar
	var cleanup func()
	httpGetFn, cleanup = fakeTarHandlerFunc("fake-tar-content")
	t.Cleanup(cleanup)
	osStatFn, osReadDirFn, osMkdirAllFn, osRemoveAllFn, osRenameFn = os.Stat, os.ReadDir, os.MkdirAll, os.RemoveAll, os.Rename

	root := filepath.Join(t.TempDir(), "public_html")
	if err := os.MkdirAll(filepath.Join(root, "wp-content", "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.php"), []byte("TENANT"), 0o644); err != nil {
		t.Fatal(err)
	}
	names := func() string {
		ents, _ := os.ReadDir(root)
		var n []string
		for _, e := range ents {
			n = append(n, e.Name())
		}
		return strings.Join(n, ",")
	}

	var log strings.Builder
	if err := downloadAndExtract(root, &log); err == nil {
		t.Fatal("extract over a non-empty wp-content succeeded")
	}
	if got := names(); got != "index.php,wp-content" {
		t.Errorf("failed extract left entries %s, want the root unchanged (index.php,wp-content)", got)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "index.php")); string(b) != "TENANT" {
		t.Errorf("failed extract replaced index.php with %q", b)
	}

	if err := os.Rename(filepath.Join(root, "wp-content"), filepath.Join(filepath.Dir(root), "wp-content.old")); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := downloadAndExtract(root, &log); err != nil {
		t.Fatalf("retry after moving wp-content aside: %v", err)
	}
	if got := names(); got != "index.php,wp-admin,wp-content,wp-includes" {
		t.Errorf("retry left entries %s", got)
	}
}
