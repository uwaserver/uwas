//go:build linux

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// shutdownTestPHPAlive reports whether pid is still a live (non-zombie)
// process, allowing up to 3s for a SIGKILLed one to be torn down.
func shutdownTestPHPAlive(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return false
		}
		s := string(b)
		if i := strings.LastIndex(s, ")"); i >= 0 && i+2 < len(s) && s[i+2] == 'Z' {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(10 * time.Millisecond) // poll interval for the condition only
	}
}

// shutdownTestFakePHP writes a fake php-cgi that records its pid and blocks,
// and returns a func that waits for that pid.
func shutdownTestFakePHP(t *testing.T, dir, name string) (string, func() int) {
	t.Helper()
	pidFile := filepath.Join(dir, name+".pid")
	bin := filepath.Join(dir, name, "php-cgi")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho $$ > " + pidFile + "\nexec /bin/sleep 120\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, func() int {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if b, err := os.ReadFile(pidFile); err == nil {
				if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil {
					t.Cleanup(func() {
						_ = syscall.Kill(-pid, syscall.SIGKILL)
						_ = syscall.Kill(pid, syscall.SIGKILL)
					})
					return pid
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("fake php-cgi %s never started", name)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// F66300: shutdown stopped only per-domain PHP workers; the per-version
// workers started from the dashboard (StartFPM) run in their own process
// group, so outside a cgroup kill they outlived UWAS, untracked.
func TestShutdownStopsGlobalPHPWorkers(t *testing.T) {
	dir := t.TempDir()
	globalBin, globalPid := shutdownTestFakePHP(t, dir, "global")
	domainBin, domainPid := shutdownTestFakePHP(t, dir, "domain")

	cfg := &config.Config{Global: config.GlobalConfig{
		LogLevel: "error", LogFormat: "text",
		Timeouts: config.TimeoutConfig{ShutdownGrace: config.Duration{Duration: time.Second}},
	}}
	s := New(cfg, logger.New("error", "text"))
	s.phpMgr.SetInstallationsForTesting([]phpmanager.PHPInstall{
		{Version: "8.9.1", Binary: globalBin, SAPI: "cgi-fcgi"},
		{Version: "8.8.1", Binary: domainBin, SAPI: "cgi-fcgi"},
	})
	if err := s.phpMgr.StartFPM("8.9.1", "127.0.0.1:0"); err != nil {
		t.Fatalf("StartFPM: %v", err)
	}
	if _, err := s.phpMgr.AssignDomainWithRoot("ctl.test", "8.8.1", dir); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := s.phpMgr.StartDomain("ctl.test"); err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	gp, dp := globalPid(), domainPid()

	s.shutdown()

	if shutdownTestPHPAlive(dp) {
		t.Fatalf("domain PHP worker (pid %d) survived shutdown", dp)
	}
	if shutdownTestPHPAlive(gp) {
		t.Fatalf("global PHP worker (pid %d) survived shutdown", gp)
	}
	for _, st := range s.phpMgr.Status() {
		if st.Running {
			t.Fatalf("Status still reports PHP %s running after shutdown", st.Version)
		}
	}
}
