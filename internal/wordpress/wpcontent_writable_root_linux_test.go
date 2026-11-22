//go:build linux

package wordpress

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWPContentWritableAsRootMatchesWebAccount runs only as root (the case
// F68600 is about): a root:root 0755 wp-content, which the web account PHP
// runs as cannot write, must not be reported writable.
func TestWPContentWritableAsRootMatchesWebAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: a root UWAS writing as itself is the defect")
	}
	web, err := webAccountIdentity()
	if err != nil {
		t.Skip("no web account:", err)
	}
	site, err := os.MkdirTemp("/tmp", "uwas-wpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(site) })
	if err := os.Chmod(site, 0o755); err != nil {
		t.Fatal(err)
	}
	wpc := filepath.Join(site, "wp-content")
	if err := os.Mkdir(wpc, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, owned := range []bool{false, true} {
		if owned {
			if err := os.Chown(wpc, web.uid, web.gid); err != nil {
				t.Fatal(err)
			}
		}
		probe := exec.Command("/bin/sh", "-c", `touch "$1/.probe" && rm -f "$1/.probe"`, "sh", wpc)
		probe.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(web.uid), Gid: uint32(web.gid)}}
		real := probe.Run() == nil
		if got := checkPermissions(site).Writable; got != real {
			t.Errorf("wp-content owned by web account=%v: Writable = %v, but the web account (uid %d) can write: %v", owned, got, web.uid, real)
		}
	}
}
