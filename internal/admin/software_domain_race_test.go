package admin

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func softwareAttachedHosts(s *Server, port int) []string {
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

func softwareGoroutineParked(fn string) bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, fn) && (strings.Contains(g, "sync.(*Mutex).Lock") || strings.Contains(g, "sync.runtime_SemacquireMutex")) {
			return true
		}
	}
	return false
}

// TestSoftwareInstallRejectsExistingDomainBeforeUp covers F482: a domain that
// already exists must be rejected before compose runs, so the metadata never
// records a domain that was not attached.
func TestSoftwareInstallRejectsExistingDomainBeforeUp(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	calls := stubSoftwareCompose(t, "id\n", 0)

	body := strings.NewReader(fmt.Sprintf(`{"template_id":"uptime-kuma","name":"kuma","host_port":%d,"domain":"api.example.com"}`, freeTCPPort(t)))
	rec := httptest.NewRecorder()
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", body)))
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("compose should not run on a domain conflict: %#v", *calls)
	}
	if _, err := os.Stat(filepath.Join(root, "kuma")); !os.IsNotExist(err) {
		t.Fatalf("instance dir should not exist, stat err = %v", err)
	}
}

// TestSoftwareDomainConnectConcurrentNoOrphan covers F483: while one connect
// is inside attach (held by the domain-change hook), a second connect must
// wait, so the end state attaches exactly the domain the metadata records.
func TestSoftwareDomainConnectConcurrentNoOrphan(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	port := freeTCPPort(t)
	dir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{Name: "kuma", TemplateID: "uptime-kuma", Dir: dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"), HasWeb: true, WebPort: 3001, HostPort: port}
	if err := os.WriteFile(inst.ComposeFile, []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	calls := 0
	entered, release := make(chan struct{}), make(chan struct{})
	s.SetOnDomainChange(func() {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			close(entered)
			<-release
		}
	})
	connect := func(host string) int {
		req := httptest.NewRequest("POST", "/api/v1/software/kuma/domain", strings.NewReader(`{"domain":"`+host+`"}`))
		req.SetPathValue("name", "kuma")
		rec := httptest.NewRecorder()
		s.handleSoftwareDomainConnect(rec, withAdminContext(req))
		return rec.Code
	}

	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() { defer wg.Done(); codes[0] = connect("one.example.net") }()
	<-entered
	bDone := make(chan struct{})
	go func() { defer wg.Done(); codes[1] = connect("two.example.net"); close(bDone) }()
	for !softwareGoroutineParked("handleSoftwareDomainConnect") {
		select {
		case <-bDone:
			close(release)
			wg.Wait()
			t.Fatal("second connect completed while the first was still attaching")
		default:
		}
		runtime.Gosched()
	}
	close(release)
	wg.Wait()

	final, err := loadSoftwareInstance("kuma")
	if err != nil {
		t.Fatal(err)
	}
	attached := softwareAttachedHosts(s, port)
	if codes[0] != 200 || codes[1] != 200 || final.Domain != "two.example.net" || len(attached) != 1 || attached[0] != "two.example.net" {
		t.Fatalf("codes=%v meta=%q attached=%v; want one attached domain matching metadata", codes, final.Domain, attached)
	}
}
