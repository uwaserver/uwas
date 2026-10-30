package server

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func startOnBusyPort(t *testing.T, pidFile string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	cfg := &config.Config{}
	cfg.Global.HTTPListen = ln.Addr().String()
	cfg.Global.PIDFile = pidFile
	s := New(cfg, logger.New("error", "text"))
	if err := s.Start(); err == nil {
		t.Fatal("Start() succeeded on an occupied port")
	}
}

// F56301: a second instance that fails to bind used to overwrite the PID file
// of the running one with its own (soon dead) PID, so `uwas stop`/`reload`
// then reported "not running" for a live server.
func TestFailedStartKeepsRunningInstancesPIDFile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "uwas.pid")
	if err := os.WriteFile(pidFile, []byte("424242"), 0644); err != nil {
		t.Fatal(err)
	}
	startOnBusyPort(t, pidFile)
	got, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("running instance's PID file removed: %v", err)
	}
	if string(got) != "424242" {
		t.Fatalf("PID file = %q, want the running instance's 424242", got)
	}
}

// Control: a failed start with no prior PID file leaves none behind.
func TestFailedStartLeavesNoPIDFile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "uwas.pid")
	startOnBusyPort(t, pidFile)
	if _, err := os.Stat(pidFile); err == nil {
		t.Fatal("failed start left a PID file behind")
	}
}

// removePID only deletes a PID file that still names this process.
func TestRemovePIDOnlyOwnFile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "uwas.pid")
	cfg := &config.Config{}
	cfg.Global.PIDFile = pidFile
	s := New(cfg, logger.New("error", "text"))

	os.WriteFile(pidFile, []byte("424242"), 0644)
	s.removePID()
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatal("removePID deleted another instance's PID file")
	}
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
	s.removePID()
	if _, err := os.Stat(pidFile); err == nil {
		t.Fatal("removePID kept its own PID file")
	}
}
