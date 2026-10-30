package phpmanager

import (
	"fmt"
	"os/user"
	"strconv"
)

// workerIdentity is the unprivileged account tenant PHP runs as.
type workerIdentity struct {
	uid, gid int
}

// workerIdentityFor reports the account php-cgi workers must run as, or nil
// when they keep UWAS's own identity (UWAS is not root, so it could not change
// it anyway). It uses the same rule as the php-fpm pool (fpmPoolUser): a
// php-cgi master started by a root UWAS used to run every tenant's PHP as
// root, while the generated php-fpm pool dropped to the web account (F42700).
// A root UWAS that cannot resolve the account refuses to start PHP rather than
// run it as root.
func workerIdentityFor() (*workerIdentity, error) {
	name, ok := fpmPoolUser()
	if !ok {
		return nil, nil
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("PHP worker account %q: %w", name, err)
	}
	uid, uerr := strconv.Atoi(u.Uid)
	gid, gerr := strconv.Atoi(u.Gid)
	if uerr != nil || gerr != nil {
		return nil, fmt.Errorf("PHP worker account %q has a non-numeric uid/gid (%q/%q)", name, u.Uid, u.Gid)
	}
	return &workerIdentity{uid: uid, gid: gid}, nil
}
