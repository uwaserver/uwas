package cli

import (
	"errors"
	"os/exec"
	"testing"
)

func TestDetectConflictsFindsActiveServiceOutsidePath(t *testing.T) {
	originalGOOS := conflictsRuntimeGOOS
	originalLookPath := conflictsExecLookPath
	originalCommand := conflictsExecCommand
	t.Cleanup(func() {
		conflictsRuntimeGOOS = originalGOOS
		conflictsExecLookPath = originalLookPath
		conflictsExecCommand = originalCommand
	})

	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(string) (string, error) {
		return "", errors.New("not on PATH")
	}
	conflictsExecCommand = func(name string, args ...string) *exec.Cmd {
		if name == "systemctl" && len(args) >= 2 && args[0] == "is-active" && args[1] == "nginx" {
			return exec.Command("sh", "-c", "printf active")
		}
		if name == "systemctl" && len(args) >= 5 && args[0] == "show" && args[4] == "nginx" {
			return exec.Command("sh", "-c", "printf 4242")
		}
		return exec.Command("sh", "-c", "exit 1")
	}

	conflicts := DetectConflicts()
	if len(conflicts) != 1 || conflicts[0].Name != "Nginx" || !conflicts[0].Running || conflicts[0].PID != "4242" {
		t.Fatalf("active nginx outside PATH was missed: %+v", conflicts)
	}
}
