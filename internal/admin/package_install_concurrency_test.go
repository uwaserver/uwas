package admin

import (
	"net/http/httptest"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/install"
)

// packageInstallParked counts goroutines blocked on a sync primitive inside
// handlePackageInstall.
func packageInstallParked() int {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		if strings.Contains(g, "handlePackageInstall") && strings.Contains(header, "[sync.") {
			n++
		}
	}
	return n
}

// Concurrent package installs must not both pass the "another installation
// in progress" guard. Each request is parked between its Active() check and
// Submit() (recordAuditR takes auditMu in between), the worker is occupied so
// queued tasks stay active, and then all requests are released together.
func TestPackageInstallConcurrentRequestsQueueOnce(t *testing.T) {
	restore := setSystemExecCommand(func(string, ...string) *exec.Cmd { return exec.Command("true") })
	defer restore()

	for _, n := range []int{2, 8} {
		s := testServer()
		q := install.New()
		s.taskMgr = q

		s.auditMu.Lock()
		var wg sync.WaitGroup
		codes := make([]int, n)
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rec := httptest.NewRecorder()
				s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/packages/install",
					strings.NewReader(`{"id":"webp"}`))))
				codes[i] = rec.Code
			}(i)
		}
		for packageInstallParked() < n {
			runtime.Gosched()
		}
		started, release := make(chan struct{}), make(chan struct{})
		q.Submit("gate", "gate", "install", func(func(string)) error { close(started); <-release; return nil })
		<-started
		s.auditMu.Unlock()
		wg.Wait()

		tasks := 0
		for _, tk := range q.List() {
			if tk.Type == "package" {
				tasks++
			}
		}
		close(release)
		q.Stop()

		sort.Ints(codes)
		if tasks != 1 || codes[0] != 200 || codes[n-1] != 409 || (n > 1 && codes[1] != 409) {
			t.Fatalf("n=%d: codes=%v package tasks=%d, want one 200, rest 409, 1 task", n, codes, tasks)
		}
	}
}
