//go:build windows

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32              = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess          = modkernel32.NewProc("OpenProcess")
	procGetExitCodeProcess   = modkernel32.NewProc("GetExitCodeProcess")
)

const processQueryLimitedInfo = 0x1000

func isProcessAlive(proc *os.Process) bool {
	h, _, err := procOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(proc.Pid))
	if h == 0 || err != nil && err != syscall.Errno(0) {
		return false
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	var exitCode uint32
	r, _, _ := procGetExitCodeProcess.Call(h, uintptr(unsafe.Pointer(&exitCode)))
	if r == 0 {
		return false
	}
	// STILL_ACTIVE (259) means the process hasn't exited yet.
	return exitCode == 259
}
