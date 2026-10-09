package admin

import (
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/install"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// setupRequeueServer returns a server whose install queue worker is held by a
// running task, so nothing submitted by the test ever executes.
func setupRequeueServer(t *testing.T, gateType, gateName string) (*Server, *install.Queue) {
	t.Helper()
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))
	q := install.New()
	s.taskMgr = q
	started, release := make(chan struct{}), make(chan struct{})
	q.Submit(gateType, gateName, "install", func(func(string)) error { close(started); <-release; return nil })
	<-started
	t.Cleanup(func() { q.Stop(); close(release) })
	return s, q
}

func setupRequeuePost(t *testing.T, s *Server, body string) (int, []setupInstallResult) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, setupInstallReq(body))
	var resp struct {
		Items  []setupInstallResult `json:"items"`
		Queued int                  `json:"queued"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	return resp.Queued, resp.Items
}

func queuedInstalls(q *install.Queue, typ, name string) (n int) {
	for _, tk := range q.List() {
		if tk.Type == typ && tk.Name == name && tk.Action == "install" && tk.Status == install.StatusQueued {
			n++
		}
	}
	return n
}

// TestSetupInstallSkipsAlreadyQueued: re-submitting the wizard while an item is
// still queued or running must not queue the same installer again.
func TestSetupInstallSkipsAlreadyQueued(t *testing.T) {
	s, q := setupRequeueServer(t, "php", "8.2")
	if n, _ := setupRequeuePost(t, s, `{"items":[{"type":"php","id":"8.3"}]}`); n != 1 {
		t.Fatalf("first submit queued %d, want 1", n)
	}
	n, items := setupRequeuePost(t, s, `{"items":[{"type":"php","id":"8.3"},{"type":"php","id":"8.2"},{"type":"php","id":"8.4"}]}`)
	if n != 1 || queuedInstalls(q, "php", "8.3") != 1 {
		t.Fatalf("re-submit queued %d (8.3 queued x%d), want only 8.4", n, queuedInstalls(q, "php", "8.3"))
	}
	for _, it := range items[:2] {
		if !it.Skipped || it.Reason != "already queued" {
			t.Errorf("%s: skipped=%v reason=%q, want already queued", it.ID, it.Skipped, it.Reason)
		}
	}
}

// TestSetupInstallConcurrentDoubleSubmit holds the wizard lock until both
// requests are waiting on it, then releases them: exactly one may queue.
func TestSetupInstallConcurrentDoubleSubmit(t *testing.T) {
	s, q := setupRequeueServer(t, "gate", "gate")
	setupInstallMu.Lock()
	var wg sync.WaitGroup
	res := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); res[i], _ = setupRequeuePost(t, s, `{"items":[{"type":"php","id":"8.5"}]}`) }()
	}
	for {
		buf := make([]byte, 1<<20)
		if strings.Count(string(buf[:runtime.Stack(buf, true)]), "admin.(*Server).handleSetupInstall") >= 2 {
			break
		}
		runtime.Gosched()
	}
	setupInstallMu.Unlock()
	wg.Wait()
	if res[0]+res[1] != 1 || queuedInstalls(q, "php", "8.5") != 1 {
		t.Fatalf("queued %v (8.5 queued x%d), want exactly one", res, queuedInstalls(q, "php", "8.5"))
	}
}
