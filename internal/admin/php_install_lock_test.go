package admin

import (
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/install"
)

// phpInstallBlocked reports whether a goroutine whose stack contains fn is
// parked on a sync primitive.
func phpInstallBlocked(fn string) bool {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		if strings.Contains(g, fn) && strings.Contains(header, "[sync.") {
			return true
		}
	}
	return false
}

func phpInstallPost(s *Server, path, body string) int {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("POST", path, strings.NewReader(body))))
	return rec.Code
}

func phpInstallTasks(q *install.Queue) int {
	n := 0
	for _, tk := range q.List() {
		if tk.Type == "php" {
			n++
		}
	}
	return n
}

// TestPHPInstallSerializedWithOtherInstalls covers F580: the PHP install
// endpoint must take setupInstallMu across its Active() check and Submit(),
// like package installs, the setup wizard and the database install.
func TestPHPInstallSerializedWithOtherInstalls(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	restore := setSystemExecCommand(func(string, ...string) *exec.Cmd { return exec.Command("/bin/true") })
	defer restore()

	gate := make(chan struct{})
	restoreRun := swapPHPRunInstall(func(string) (string, error) { <-gate; return "stub", nil })
	var queues []*install.Queue
	defer func() {
		close(gate)
		for _, q := range queues {
			for q.Active() != nil {
				runtime.Gosched()
			}
			q.Stop()
		}
		restoreRun()
	}()
	newServer := func() (*Server, *install.Queue) {
		s := testServer()
		if s.phpHandler == nil {
			s.initPHPHandler()
		}
		q := install.New()
		s.taskMgr = q
		queues = append(queues, q)
		return s, q
	}

	t.Run("package install between check and submit", func(t *testing.T) {
		s, q := newServer()
		// recordAuditR takes auditMu between the package handler's Active()
		// check and its Submit(); holding it parks that request there.
		s.auditMu.Lock()
		pkgDone := make(chan int, 1)
		go func() { pkgDone <- phpInstallPost(s, "/api/v1/packages/install", `{"id":"mariadb"}`) }()
		for !phpInstallBlocked("handlePackageInstall") {
			runtime.Gosched()
		}
		phpDone := make(chan int, 1)
		go func() { phpDone <- phpInstallPost(s, "/api/v1/php/install", `{"version":"8.3"}`) }()
		phpCode := 0
		for phpCode == 0 {
			select {
			case phpCode = <-phpDone:
			default:
				if phpInstallBlocked("php.(*Handler).Install") {
					phpCode = -1
				}
				runtime.Gosched()
			}
		}
		s.auditMu.Unlock()
		pkgCode := <-pkgDone
		if phpCode == -1 {
			phpCode = <-phpDone
		}
		if pkgCode != 200 || phpCode != 409 || phpInstallTasks(q) != 0 {
			t.Fatalf("package=%d php=%d phpTasks=%d, want 200/409/0", pkgCode, phpCode, phpInstallTasks(q))
		}
	})

	t.Run("concurrent PHP installs", func(t *testing.T) {
		s, q := newServer()
		start := make(chan struct{})
		codes := make(chan int, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				codes <- phpInstallPost(s, "/api/v1/php/install", `{"version":"8.3"}`)
			}()
		}
		close(start)
		wg.Wait()
		close(codes)
		ok := 0
		for c := range codes {
			if c == 200 {
				ok++
			}
		}
		if ok != 1 || phpInstallTasks(q) != 1 {
			t.Fatalf("accepted=%d phpTasks=%d, want exactly 1", ok, phpInstallTasks(q))
		}
	})
}
