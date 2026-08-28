package phpmanager

import "errors"

var errCgroupUnavailable = errors.New("cgroup v2 not available")

// rlimitTestHooks swaps the rlimit indirection and returns a restore func.
func rlimitTestHooks() func() {
	origApply, origAssign, origRemove := rlimitApply, rlimitAssignPID, rlimitRemove
	return func() {
		rlimitApply, rlimitAssignPID, rlimitRemove = origApply, origAssign, origRemove
	}
}
