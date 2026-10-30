//go:build !unix

package phpmanager

import "os/exec"

// Identities are a unix concept; workerIdentityFor never yields one elsewhere
// because fpmPoolUser requires euid 0.
func applyWorkerIdentity(cmd *exec.Cmd, id *workerIdentity) {}
