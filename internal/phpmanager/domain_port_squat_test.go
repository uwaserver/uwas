//go:build unix

package phpmanager

import (
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// F43301: a domain's PHP pool is a bare FastCGI port on 127.0.0.1 and UWAS
// sends every request for the domain there - headers, cookies, bodies. The
// port was chosen from UWAS's own tables only, never checked against the
// machine, and StartDomain handed a persisted address to php-cgi without
// looking at it. A process that already listens there (another tenant's PHP
// script via stream_socket_server, an app, a database container) therefore
// either made php-cgi fail to bind or, worse, kept receiving the domain's
// traffic while UWAS believed the pool was its own. Both paths now pass over a
// port that something else holds.

// realPortProbe switches the port probe back on for the test (TestMain turns it
// off for the rest of the suite).
func realPortProbe(t *testing.T) {
	t.Helper()
	old := domainPortFreeFn
	domainPortFreeFn = loopbackPortFree
	t.Cleanup(func() { domainPortFreeFn = old })
}

// heldPort listens on a free loopback port and returns it with the listener.
func heldPort(t *testing.T) (int, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port, ln
}

// freePort returns a loopback port nothing listens on right now.
func freePort(t *testing.T) int {
	t.Helper()
	p, ln := heldPort(t)
	ln.Close()
	return p
}

func TestAssignDomainSkipsPortHeldByAnotherProcess(t *testing.T) {
	realPortProbe(t)
	held, _ := heldPort(t)
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"}}
	m.nextPort = held

	dp, err := m.AssignDomain("victim.test", "8.4")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: an address other than 127.0.0.1:%d (held by another process)\nACTUAL: %s", held, dp.ListenAddr)
	if dp.ListenAddr == "127.0.0.1:"+strconv.Itoa(held) {
		t.Fatalf("PROBLEM CONFIRMED: domain assigned port %d although another process listens on it", held)
	}
}

// Control: a port nothing holds is still handed out as before.
func TestAssignDomainUsesFreePort(t *testing.T) {
	realPortProbe(t)
	free := freePort(t)
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"}}
	m.nextPort = free
	dp, err := m.AssignDomain("ok.test", "8.4")
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:" + strconv.Itoa(free); dp.ListenAddr != want {
		t.Fatalf("addr = %s, want %s", dp.ListenAddr, want)
	}
}

func startDomainOn(t *testing.T, addr string) (workerAddr string, m *Manager, notified *string) {
	t.Helper()
	m = New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: filepath.Join(t.TempDir(), "php-cgi"), SAPI: "cgi-fcgi"}}
	var got []string
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		got = args
		return exec.Command("sleep", "100")
	}
	var seen string
	m.SetDomainChangeFunc(func(domain, listenAddr string) { seen = listenAddr })
	m.RegisterExistingDomain("tenant.test", "8.4", addr, t.TempDir(), nil)
	if err := m.StartDomain("tenant.test"); err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	t.Cleanup(func() { _ = m.StopDomain("tenant.test") })
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "-b" {
			workerAddr = got[i+1]
		}
	}
	return workerAddr, m, &seen
}

func TestStartDomainDoesNotUsePortHeldByAnotherProcess(t *testing.T) {
	realPortProbe(t)
	held, _ := heldPort(t)
	heldAddr := "127.0.0.1:" + strconv.Itoa(held)

	workerAddr, m, notified := startDomainOn(t, heldAddr)
	t.Logf("EXPECTED: php-cgi bound somewhere other than %s\nACTUAL: -b %s (domain now reports %s)", heldAddr, workerAddr, m.RunningAddrForDomain("tenant.test"))
	if workerAddr == "" {
		t.Fatal("no -b argument reached php-cgi")
	}
	if workerAddr == heldAddr {
		t.Fatalf("PROBLEM CONFIRMED: php-cgi told to bind %s, which another process already holds", heldAddr)
	}
	// The address UWAS routes to must be the one the worker was given.
	if got := m.RunningAddrForDomain("tenant.test"); got != workerAddr {
		t.Errorf("domain routes to %s but the worker listens on %s", got, workerAddr)
	}
	if *notified != workerAddr {
		t.Errorf("config was told %q, want the worker's address %q", *notified, workerAddr)
	}
}

// Control: a free persisted address is kept as is.
func TestStartDomainKeepsFreePersistedPort(t *testing.T) {
	realPortProbe(t)
	addr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	workerAddr, _, notified := startDomainOn(t, addr)
	if workerAddr != addr || *notified != addr {
		t.Fatalf("worker -b %q, notified %q; want %q for both", workerAddr, *notified, addr)
	}
}
