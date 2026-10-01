package phpmanager

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The php-fpm config interpolates listenAddr straight into an INI file
// (`listen = %s`). php-fpm INI is last-wins, so a newline in an
// admin-supplied listen_addr — internal/admin/php/handler.go passes
// req.ListenAddr through after only an emptiness check — appends further
// pool directives. An injected `user = root` overrides the unprivileged
// identity fpmPoolUser exists to select, and a second `listen` binds the
// unauthenticated FastCGI port to a public interface. That is remote code
// execution as root.
func TestStartFPMDaemonRejectsControlCharsInListenAddr(t *testing.T) {
	attacks := []struct {
		name string
		addr string
	}{
		{"root override and public listen", "127.0.0.1:9000\nuser = root\ngroup = root\nlisten = 0.0.0.0:9001"},
		{"bare newline then directive", "127.0.0.1:9000\nuser = root"},
		{"carriage return", "127.0.0.1:9000\r\nuser = root"},
		{"leading newline", "\nuser = root\nlisten = 0.0.0.0:9001"},
		{"null byte", "127.0.0.1:9000\x00\nuser = root"},
		{"section escape to global", "127.0.0.1:9000\n[global]\nerror_log = /dev/null"},
	}
	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			conf, err := captureFPMConfigNoWrite(t, tc.addr)
			if err != nil {
				return // refused: nothing written, nothing injectable
			}
			dirs := wwwPoolDirectives(conf)
			if dirs["user"] == "root" || dirs["group"] == "root" {
				t.Errorf("php-fpm pool user/group = root via listen_addr injection; %v", dirs)
			}
			if dirs["listen"] == "0.0.0.0:9001" {
				t.Errorf("injected a public listen address; %v", dirs)
			}
			if n := countListenDirectives(conf); n != 1 {
				t.Errorf("[www] carries %d listen directives, want 1:\n%s", n, conf)
			}
		})
	}
}

// Controls: the legitimate listen forms must keep working. domain.go:58 shows
// a real caller passes "unix:/run/php.sock", and a bare socket path is also
// valid, so validation must not narrow the address to host:port.
func TestStartFPMDaemonAcceptsLegitimateListenAddrs(t *testing.T) {
	cases := []struct {
		addr  string
		listen string
	}{
		{"127.0.0.1:9000", "127.0.0.1:9000"},
		{"127.0.0.1:9123", "127.0.0.1:9123"},
		{"[::1]:9000", "[::1]:9000"},
		{"unix:/run/php.sock", "unix:/run/php.sock"},
		{"/run/php/php8.4-fpm.sock", "/run/php/php8.4-fpm.sock"},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			conf, err := captureFPMConfigNoWrite(t, tc.addr)
			if err != nil {
				t.Fatalf("legitimate listen address %q was rejected: %v", tc.addr, err)
			}
			dirs := wwwPoolDirectives(conf)
			if dirs["listen"] != tc.listen {
				t.Errorf("listen = %q, want %q; %v", dirs["listen"], tc.listen, dirs)
			}
			if n := countListenDirectives(conf); n != 1 {
				t.Errorf("[www] carries %d listen directives, want 1; %s", n, conf)
			}
			// The unprivileged pool identity must still be present when the
			// host supplies one — that is what the guard must not have cost.
			if dirs["user"] == "root" {
				t.Errorf("pool user = root; %v", dirs)
			}
		})
	}
}

// validListenAddr unit boundary, including the empty case the daemon must
// refuse outright.
func TestValidListenAddr(t *testing.T) {
	good := []string{"127.0.0.1:9000", "0.0.0.0:9000", "[::1]:9000", "unix:/run/php.sock", "/run/php.sock", "a-b_c/d.sock"}
	bad := []string{"", "\n", "\r", "\x00", "\t", "127.0.0.1:9000\nuser = root", "foo\x7fbar", "\x1b[31m"}
	for _, a := range good {
		if !validListenAddr(a) {
			t.Errorf("validListenAddr(%q) = false, want true", a)
		}
	}
	for _, a := range bad {
		if validListenAddr(a) {
			t.Errorf("validListenAddr(%q) = true, want false", a)
		}
	}
}

// captureFPMConfigNoWrite drives the real startFPMDaemon and returns the config
// it wrote, or the error it refused with. The package's own seams are used
// (osWriteFileHook for the write, m.execCommand so no real php-fpm spawns),
// matching the existing TestStartFPMDaemonPoolUser.
func captureFPMConfigNoWrite(t *testing.T, listenAddr string) (string, error) {
	t.Helper()

	origWrite := osWriteFileHook
	origUser := fpmPoolUser
	defer func() { osWriteFileHook = origWrite; fpmPoolUser = origUser }()

	fpmPoolUser = func() (string, bool) { return "www-data", true }

	var written string
	osWriteFileHook = func(name string, data []byte, perm os.FileMode) error {
		if strings.Contains(name, "fpm.conf") {
			written = string(data)
		}
		return nil
	}

	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sleep", "100")
	}
	defer func() {
		if val, ok := m.processes.Load("8.4.19"); ok {
			if info, ok := val.(*processInfo); ok && info.cmd != nil && info.cmd.Process != nil {
				_ = info.cmd.Process.Kill()
			}
			m.processes.Delete("8.4.19")
		}
	}()

	if err := m.startFPMDaemon("8.4.19", "/usr/sbin/php-fpm8.4", listenAddr); err != nil {
		return "", err
	}
	if written == "" {
		t.Fatal("no fpm config was written")
	}
	return written, nil
}

// wwwPoolDirectives parses the [www] section into a last-wins directive map —
// the effective configuration, as php-fpm itself reads it.
func wwwPoolDirectives(conf string) map[string]string {
	out := map[string]string{}
	inWWW := false
	for _, line := range strings.Split(conf, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inWWW = trimmed == "[www]"
			continue
		}
		if !inWWW || trimmed == "" {
			continue
		}
		if k, v, ok := strings.Cut(trimmed, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func countListenDirectives(conf string) int {
	n := 0
	inWWW := false
	for _, line := range strings.Split(conf, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inWWW = trimmed == "[www]"
			continue
		}
		if inWWW && strings.HasPrefix(trimmed, "listen") {
			n++
		}
	}
	return n
}
