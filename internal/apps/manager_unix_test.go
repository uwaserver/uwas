//go:build !windows

package apps

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStopKillsNativeProcessGroupChildren(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "child.pid")
	mgr := NewManager(NewStore(dir), nil)
	app := &App{
		Name:    "tree-stop",
		Runtime: RuntimeCustom,
		Command: "sleep 30 & echo $! > child.pid; wait",
		WorkDir: dir,
	}
	if err := mgr.Register(app); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.Start(app.Name); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer mgr.Stop(app.Name)

	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil {
				childPID = pid
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child pid was not written")
	}
	if err := syscall.Kill(childPID, syscall.Signal(0)); err != nil {
		t.Fatalf("child process should be alive before stop: %v", err)
	}

	if err := mgr.Stop(app.Name); err != nil {
		t.Fatalf("stop: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child process %d survived Stop; process tree was not killed", childPID)
}
