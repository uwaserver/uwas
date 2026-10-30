package wordpress

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// runIdentity is the unprivileged account wp-cli runs as.
type runIdentity struct {
	uid, gid int
}

// wpRunIdentityFn picks the account wp-cli runs as. Overridable in tests.
var wpRunIdentityFn = wpRunIdentity

// wpRunIdentity reports the account wp-cli must run as for the site at webRoot,
// or nil when it keeps UWAS's own identity (UWAS is not root, so it could not
// change it anyway).
//
// wp-cli loads the site's wp-config.php, mu-plugins, plugins and themes
// in-process. Those are tenant-controlled PHP files, and a root UWAS used to
// run them as root: a WordPress admin (or an SFTP user) who dropped a plugin
// got root the next time the operator opened the site's plugin list (F43000).
// The site's own owner is the natural account, as it is for the PHP worker;
// a root-owned (or unknown) web root falls back to the web account. A root
// UWAS that cannot resolve an account refuses to run wp-cli instead of
// running it as root.
func wpRunIdentity(webRoot string) (*runIdentity, error) {
	uid, gid, ok := pathOwner(webRoot)
	return chooseRunIdentity(os.Geteuid(), uid, gid, ok, webAccountIdentity)
}

// chooseRunIdentity is the decision of wpRunIdentity with its inputs made
// explicit: the process's effective uid, the web root's owner, and the lookup
// of the web account.
func chooseRunIdentity(euid, ownerUID, ownerGID int, ownerKnown bool, web func() (*runIdentity, error)) (*runIdentity, error) {
	if euid != 0 {
		return nil, nil
	}
	if ownerKnown && ownerUID != 0 {
		return &runIdentity{uid: ownerUID, gid: ownerGID}, nil
	}
	return web()
}

// webAccountIdentity resolves the conventional web account, falling back to the
// universally present "nobody" (the same rule the php-fpm pool uses).
func webAccountIdentity() (*runIdentity, error) {
	name := "nobody"
	for _, n := range []string{"www-data", "nginx", "apache", "http"} {
		if _, err := user.Lookup(n); err == nil {
			name = n
			break
		}
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("wp-cli account %q: %w", name, err)
	}
	uid, uerr := strconv.Atoi(u.Uid)
	gid, gerr := strconv.Atoi(u.Gid)
	if uerr != nil || gerr != nil {
		return nil, fmt.Errorf("wp-cli account %q has a non-numeric uid/gid (%q/%q)", name, u.Uid, u.Gid)
	}
	return &runIdentity{uid: uid, gid: gid}, nil
}
