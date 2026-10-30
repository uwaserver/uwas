package phpmanager

import (
	"fmt"
	"os"
)

// requirePlainDir fails when dir exists as anything but a real directory. The
// per-domain session directory sits in the tenant-writable web root: a symlink
// planted there would make a root UWAS create, own and widen open_basedir to
// whatever it points at (F44000). A missing dir is fine, it is created next.
func requirePlainDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s exists but is not a plain directory (a symlink or file in the web root?); remove it so UWAS can create it", dir)
	}
	return nil
}
