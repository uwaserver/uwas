//go:build !unix

package wordpress

import "os/exec"

// Identities are a unix concept; wpRunIdentity never yields one elsewhere
// because it requires euid 0.
func pathOwner(path string) (uid, gid int, ok bool) { return 0, 0, false }

func applyRunIdentity(cmd *exec.Cmd, id *runIdentity) {}
