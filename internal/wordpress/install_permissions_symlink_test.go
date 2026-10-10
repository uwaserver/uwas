package wordpress

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// setWordPressPermissions runs as root during Install. .tmp is not part of
// the WordPress archive, so a symlink the domain user planted in the docroot
// survives extraction; chowning through it hands an arbitrary directory to
// www-data.
func TestSetWordPressPermissionsSkipsDocrootSymlinks(t *testing.T) {
	realChown, err := exec.LookPath("chown")
	if err != nil {
		t.Skip("chown not available")
	}
	if _, err := exec.LookPath("find"); err != nil {
		t.Skip("find not available")
	}
	g, err := user.LookupGroup("users")
	if err != nil || g.Gid == fmt.Sprint(os.Getgid()) {
		t.Skip("need a supplementary group named users to observe chown")
	}
	// Real chown behind a wrapper that swaps the hardcoded owner for a group
	// this unprivileged user may assign, so chown's own symlink handling decides.
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  [ \"$a\" = \"www-data:www-data\" ] && a=\":users\"\n  set -- \"$@\" \"$a\"\ndone\nshift $(( $# / 2 ))\nexec " + realChown + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "chown"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	gid := func(p string) string {
		fi, err := os.Stat(p)
		if err != nil {
			return "missing"
		}
		return fmt.Sprint(fi.Sys().(*syscall.Stat_t).Gid)
	}
	myGid := fmt.Sprint(os.Getgid())

	base := t.TempDir()
	root := filepath.Join(base, "docroot")
	tmpTarget := filepath.Join(base, "outside-tmp")
	upTarget := filepath.Join(base, "outside-uploads")
	for _, d := range []string{filepath.Join(root, "wp-content"), tmpTarget, upTarget} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	os.Symlink(tmpTarget, filepath.Join(root, ".tmp"))
	os.Symlink(upTarget, filepath.Join(root, "wp-content", "uploads"))

	var log strings.Builder
	setWordPressPermissions(root, &log)

	if got := gid(tmpTarget); got != myGid {
		t.Errorf("directory behind .tmp symlink was chowned: gid=%s, want %s", got, myGid)
	}
	if got := gid(upTarget); got != myGid {
		t.Errorf("directory behind wp-content/uploads symlink was chowned: gid=%s, want %s", got, myGid)
	}
	if got := gid(filepath.Join(root, "wp-content", "upgrade")); got != g.Gid {
		t.Errorf("regular wp-content/upgrade not chowned: gid=%s, want %s", got, g.Gid)
	}
}

// The new password must reach wp-cli on stdin, never on argv, where
// /proc/<pid>/cmdline exposes it to every local user.
func TestChangeUserPasswordKeepsPasswordOffArgv(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "wp")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + filepath.Join(dir, "argv") + "\ncat > " + filepath.Join(dir, "stdin") + "\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	old := execLookPathFn
	execLookPathFn = func(string) (string, error) { return fake, nil }
	defer func() { execLookPathFn = old }()

	const pw = "Arg-Leak-Probe 'x' = y"
	if err := ChangeUserPassword(t.TempDir(), "alice", pw); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if strings.Contains(string(argv), "Arg-Leak-Probe") {
		t.Errorf("password on wp-cli argv: %q", argv)
	}
	if string(stdin) != pw+"\n" {
		t.Errorf("stdin = %q, want %q", stdin, pw+"\n")
	}
	if err := ChangeUserPassword(t.TempDir(), "alice", "two\nlines"); err == nil {
		t.Error("password with a line break accepted; wp-cli would read only its first line")
	}
}
