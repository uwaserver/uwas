package doctor

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func dialOK(string, string, time.Duration) (net.Conn, error) { return fakeConn{}, nil }

func dialRefused(string, string, time.Duration) (net.Conn, error) { return nil, net.ErrClosed }

// php-fpm 8.5 is the current branch; its pool socket must be recognised.
func TestCheckPHPFPM_Recognises85Socket(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.5-fpm.sock": true})
	netDialTimeoutFn = dialOK
	execLookPathFn = fakeLookPath(map[string]bool{})
	execCommandFn = fakeCmd("")
	c := checkPHPFPM(false)
	if c.Status != StatusOK || !strings.Contains(c.Message, "php8.5-fpm.sock") {
		t.Fatalf("got %s %q", c.Status, c.Message)
	}
}

// The newest running branch wins when several sockets exist.
func TestCheckPHPFPM_PrefersNewestSocket(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.5-fpm.sock": true, "/run/php/php8.3-fpm.sock": true})
	netDialTimeoutFn = dialOK
	c := checkPHPFPM(false)
	if !strings.Contains(c.Message, "php8.5-fpm.sock") {
		t.Fatalf("message %q", c.Message)
	}
}

func TestCheckPHPFPM_85SocketDeadAutoFixStartsRightUnit(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.5-fpm.sock": true})
	calls := 0
	netDialTimeoutFn = func(string, string, time.Duration) (net.Conn, error) {
		calls++
		if calls == 1 {
			return nil, net.ErrClosed
		}
		return fakeConn{}, nil
	}
	var started string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if name == "systemctl" && len(args) == 2 && args[0] == "start" {
			started = args[1]
		}
		return fakeCmd("")(name, args...)
	}
	c := checkPHPFPM(true)
	if c.Status != StatusFixed || started != "php8.5-fpm" {
		t.Fatalf("status=%s started=%q", c.Status, started)
	}
}

// Installed-but-stopped 8.4/8.5 FPM is "installed but not running", not "not found",
// and the hint names that branch's unit.
func TestCheckPHPFPM_InstalledNewerBranchesNotRunning(t *testing.T) {
	for _, ver := range []string{"8.4", "8.5"} {
		saveAndRestoreHooks(t)
		osStatFn = fakeStat(map[string]bool{})
		netDialTimeoutFn = dialRefused
		execLookPathFn = fakeLookPath(map[string]bool{"php-fpm" + ver: true})
		execCommandFn = fakeCmd("")
		c := checkPHPFPM(false)
		if c.Status != StatusFail || c.Message != "php-fpm installed but not running" || !strings.Contains(c.HowTo, "php"+ver+"-fpm") {
			t.Fatalf("%s: %s %q %q", ver, c.Status, c.Message, c.HowTo)
		}
	}
}

func TestCheckPHPFPM_InstalledAutoFixStartsNewestUnit(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})
	execLookPathFn = fakeLookPath(map[string]bool{"php-fpm8.5": true, "php-fpm8.3": true})
	timeSleepFn = func(time.Duration) {}
	netDialTimeoutFn = dialOK
	var started string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if name == "systemctl" && len(args) == 2 && args[0] == "start" {
			started = args[1]
		}
		return fakeCmd("")(name, args...)
	}
	c := checkPHPFPM(true)
	if c.Status != StatusFixed || started != "php8.5-fpm" {
		t.Fatalf("status=%s started=%q", c.Status, started)
	}
}

func TestCheckPHPFPM_CGIOnlyNewerBranch(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})
	execLookPathFn = fakeLookPath(map[string]bool{"php-cgi8.4": true})
	c := checkPHPFPM(false)
	if c.Status != StatusWarn || !strings.Contains(c.HowTo, "php8.4-fpm") {
		t.Fatalf("%s %q", c.Status, c.HowTo)
	}
}

func eacces() error {
	return &net.OpError{Op: "listen", Net: "tcp", Err: &os.SyscallError{Syscall: "bind", Err: syscall.EACCES}}
}

func eaddrinuse() error {
	return &net.OpError{Op: "listen", Net: "tcp", Err: &os.SyscallError{Syscall: "bind", Err: syscall.EADDRINUSE}}
}

// A non-root run cannot bind :80/:443; that is "cannot probe", never "in use".
func TestCheckPorts_PermissionDeniedIsNotInUse(t *testing.T) {
	saveAndRestoreHooks(t)
	netListenFn = func(string, string) (net.Listener, error) { return nil, eacces() }
	c := checkPorts()
	if strings.Contains(c.Message, "in use") || !strings.Contains(c.Message, "permission denied") || c.Status != StatusWarn {
		t.Fatalf("%s %q", c.Status, c.Message)
	}
}

// Mixed outcomes stay accurate per port.
func TestCheckPorts_MixedInUseAndDenied(t *testing.T) {
	saveAndRestoreHooks(t)
	netListenFn = func(_, addr string) (net.Listener, error) {
		if addr == ":80" {
			return nil, eaddrinuse()
		}
		return nil, eacces()
	}
	c := checkPorts()
	if !strings.Contains(c.Message, "port :80 in use") || strings.Contains(c.Message, "port :443 in use") || !strings.Contains(c.Message, "port :443: cannot probe") {
		t.Fatalf("%q", c.Message)
	}
}

// A genuine bind conflict is still reported.
func TestCheckPorts_RealAddrInUseStillReported(t *testing.T) {
	saveAndRestoreHooks(t)
	netListenFn = func(string, string) (net.Listener, error) { return nil, eaddrinuse() }
	c := checkPorts()
	if c.Message != "port :80 in use; port :443 in use" {
		t.Fatalf("%q", c.Message)
	}
}
