package admin

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func softwareDeleteRaceSetup(t *testing.T, s *Server, domain string) softwareInstance {
	t.Helper()
	root := t.TempDir()
	orig := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = orig })
	port := freeTCPPort(t)
	dir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{Name: "kuma", TemplateID: "uptime-kuma", Template: "Uptime Kuma", Dir: dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"), Project: "uwas-kuma",
		HasWeb: true, WebService: "uptime-kuma", WebPort: 3001, HostPort: port, Domain: domain}
	if err := os.WriteFile(inst.ComposeFile, []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}
	if err := s.attachSoftwareDomain(domain, port); err != nil {
		t.Fatal(err)
	}
	return inst
}

// softwareDeleteRaceStubCompose stubs compose; `down` signals entered and
// blocks on gate when gate is non-nil.
func softwareDeleteRaceStubCompose(t *testing.T, entered chan<- struct{}, gate <-chan struct{}) {
	t.Helper()
	orig := softwareComposeCommand
	softwareComposeCommand = func(name string, args ...string) *exec.Cmd {
		for _, a := range args {
			if a == "down" && gate != nil {
				entered <- struct{}{}
				<-gate
			}
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { softwareComposeCommand = orig })
}

func softwareDeleteRaceProxyHosts(s *Server, port int) []string {
	up := fmt.Sprintf("http://127.0.0.1:%d", port)
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	var out []string
	for _, d := range s.config.Domains {
		if d.Type == "proxy" && len(d.Proxy.Upstreams) == 1 && d.Proxy.Upstreams[0].Address == up {
			out = append(out, d.Host)
		}
	}
	return out
}

func softwareDeleteRaceDelete(s *Server) int {
	req := httptest.NewRequest("DELETE", "/api/v1/software/kuma", nil)
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	s.handleSoftwareDelete(rec, withAdminContext(req))
	return rec.Code
}

func softwareDeleteRaceConnect(s *Server, host string) int {
	req := httptest.NewRequest("POST", "/api/v1/software/kuma/domain", strings.NewReader(`{"domain":"`+host+`"}`))
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	s.handleSoftwareDomainConnect(rec, withAdminContext(req))
	return rec.Code
}

// A domain connected while `compose down` runs must be detached by the
// delete, not left as an orphan proxy domain (F492).
func TestSoftwareDeleteDetachesDomainConnectedDuringDown(t *testing.T) {
	s := testServer()
	inst := softwareDeleteRaceSetup(t, s, "old.example.net")
	entered, gate := make(chan struct{}, 1), make(chan struct{})
	softwareDeleteRaceStubCompose(t, entered, gate)

	done := make(chan int, 1)
	go func() { done <- softwareDeleteRaceDelete(s) }()
	<-entered
	if code := softwareDeleteRaceConnect(s, "new.example.net"); code != 200 {
		t.Fatalf("connect = %d, want 200", code)
	}
	close(gate)
	if code := <-done; code != 200 {
		t.Fatalf("delete = %d, want 200", code)
	}
	if hosts := softwareDeleteRaceProxyHosts(s, inst.HostPort); len(hosts) != 0 {
		t.Fatalf("orphaned proxy domains after delete: %v", hosts)
	}
}

// Delete must wait for an in-flight connect holding softwareDomainMu and
// then detach the domain that connect recorded (F492).
func TestSoftwareDeleteWaitsForInflightConnect(t *testing.T) {
	s := testServer()
	inst := softwareDeleteRaceSetup(t, s, "old.example.net")
	softwareDeleteRaceStubCompose(t, nil, nil)

	var mu sync.Mutex
	calls := 0
	attached, release := make(chan struct{}), make(chan struct{})
	s.SetOnDomainChange(func() {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			close(attached)
			<-release
		}
	})

	cDone := make(chan int, 1)
	go func() { cDone <- softwareDeleteRaceConnect(s, "new.example.net") }()
	<-attached
	dDone := make(chan int, 1)
	go func() { dDone <- softwareDeleteRaceDelete(s) }()
	buf := make([]byte, 1<<20)
	for parked := false; !parked; {
		select {
		case code := <-dDone:
			close(release)
			<-cDone
			t.Fatalf("delete finished (%d) while connect held the domain lock", code)
		default:
		}
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "removeSoftwareInstanceRecord") &&
				(strings.Contains(g, "sync.(*Mutex).Lock") || strings.Contains(g, "runtime_SemacquireMutex")) {
				parked = true
			}
		}
		runtime.Gosched()
	}
	close(release)
	if code := <-cDone; code != 200 {
		t.Fatalf("connect = %d, want 200", code)
	}
	if code := <-dDone; code != 200 {
		t.Fatalf("delete = %d, want 200", code)
	}
	if hosts := softwareDeleteRaceProxyHosts(s, inst.HostPort); len(hosts) != 0 {
		t.Fatalf("orphaned proxy domains after delete: %v", hosts)
	}
	if _, err := os.Stat(inst.Dir); !os.IsNotExist(err) {
		t.Fatalf("instance dir still present: %v", err)
	}
}
