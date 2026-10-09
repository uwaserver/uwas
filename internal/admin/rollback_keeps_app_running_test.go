package admin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

// A deploy-core failure (failed pull/build) rolls back with restart=false; the
// rollback must not stop the still-running app and leave it offline. The
// restart=true path must still bring the app back up.
func TestRollbackDeployedAppRestartFlagKeepsAppRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := appTestServer(t)
	for _, tc := range []struct {
		name, cmd string
		restart   bool
	}{
		{"rb-norestart", "sleep 4891", false},
		{"rb-restart", "sleep 4892", true},
	} {
		a := &apps.App{Name: tc.name, Runtime: apps.RuntimeCustom, Command: tc.cmd, WorkDir: filepath.Join(t.TempDir(), "wd")}
		registerApp(t, s, a)
		cmd := tc.cmd
		t.Cleanup(func() { s.appsMgr.StopAll(); _ = exec.Command("pkill", "-f", cmd).Run() })
		if err := s.appsMgr.Start(tc.name); err != nil {
			t.Fatalf("%s: start: %v", tc.name, err)
		}
		pid := s.appsMgr.Get(tc.name).PID

		ok, _, note := s.rollbackDeployedApp(context.Background(), tc.name, a, "abc123", apps.DeployConfig{}, nil, tc.restart, &strings.Builder{})
		inst := s.appsMgr.Get(tc.name)
		if !ok || !inst.Running {
			t.Fatalf("%s: rollback ok=%v note=%q running=%v, want ok and running", tc.name, ok, note, inst.Running)
		}
		if !tc.restart && inst.PID != pid {
			t.Fatalf("%s: restart=false bounced the app (pid %d -> %d)", tc.name, pid, inst.PID)
		}
	}
}
