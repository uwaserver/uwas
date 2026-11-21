package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

type countDeps struct {
	*branchFilterDeps
	completes atomic.Int32
}

func (d *countDeps) AppCompleteDeploy(string, *apps.App, bool, string) error {
	d.completes.Add(1)
	return nil
}

// F7400: the manual deploy runs detached from the request (F7120), so a request
// queued behind the per-app lock whose client had already gone away still ran a
// full fetch, build and restart once the lock freed.
func TestQueuedDeployOfGoneClientDoesNotRun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	runOnce := func(clientGone bool) int32 {
		serveURL, _ := newServedRepo(t, "v1")
		store := apps.NewStore(t.TempDir())
		store.DataRoot = t.TempDir()
		if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, Command: "true",
			Deploy: apps.DeployConfig{GitURL: serveURL, GitBranch: "main"}}); err != nil {
			t.Fatal(err)
		}
		d := &countDeps{branchFilterDeps: &branchFilterDeps{mgr: apps.NewManager(store, nil)}}
		h := New(d)

		lock := h.getDeployLock("demo")
		lock.Lock() // a deploy of the same app is in flight
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if clientGone {
			cancel()
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/apps/demo/deploy", strings.NewReader("{}")).WithContext(ctx)
			r.SetPathValue("name", "demo")
			h.Deploy(httptest.NewRecorder(), r)
		}()
		lock.Unlock()
		<-done
		return d.completes.Load()
	}
	if got := runOnce(false); got != 1 {
		t.Fatalf("control: a live client's deploy ran %d times, want 1", got)
	}
	if got := runOnce(true); got != 0 {
		t.Fatalf("deploy of a client that had gone away ran %d times, want 0", got)
	}
}
