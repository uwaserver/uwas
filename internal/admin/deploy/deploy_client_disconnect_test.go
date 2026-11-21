package deploy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

type disconnectDeps struct {
	*branchFilterDeps
	completeCalls int
	cancel        func()
	rolledBack    bool
	rollbackErr   error
}

func (d *disconnectDeps) AppCompleteDeploy(string, *apps.App, bool, string) error {
	d.completeCalls++
	if d.completeCalls == 1 {
		return nil
	}
	if d.cancel != nil {
		d.cancel() // the client disconnects while the deploy is finishing
	}
	return errors.New("post-deploy failed")
}
func (d *disconnectDeps) AppRollback(ctx context.Context, _ string, _ *apps.App, _ string, _ apps.DeployConfig, _ map[string]string, _ bool, _ LogSink) (bool, string, string) {
	d.rolledBack = true
	d.rollbackErr = ctx.Err()
	return false, "", ""
}

// F7120: the manual deploy derived its context from the request, so a client that
// disconnects (browser closed, proxy timeout) cancels the deploy's commands and
// the automatic rollback that should undo a failed deploy runs on an already
// cancelled context.
func TestDeployRollbackSurvivesClientDisconnect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	run := func(disconnect bool) (rolledBack bool, ctxErr error) {
		serveURL, work := newServedRepo(t, "v1")
		store := apps.NewStore(t.TempDir())
		store.DataRoot = t.TempDir()
		if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, Command: "true",
			Deploy: apps.DeployConfig{GitURL: serveURL, GitBranch: "main"}}); err != nil {
			t.Fatal(err)
		}
		d := &disconnectDeps{branchFilterDeps: &branchFilterDeps{mgr: apps.NewManager(store, nil)}}
		h := New(d)
		deploy := func(ctx context.Context) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/apps/demo/deploy", strings.NewReader("{}")).WithContext(ctx)
			r.SetPathValue("name", "demo")
			rec := httptest.NewRecorder()
			h.Deploy(rec, r)
			return rec
		}
		if rec := deploy(context.Background()); !strings.Contains(rec.Body.String(), `"ok":true`) {
			t.Fatalf("first deploy failed: %s", rec.Body.String())
		}
		commitAndPush(t, work, "v2")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if disconnect {
			d.cancel = cancel
		}
		deploy(ctx)
		return d.rolledBack, d.rollbackErr
	}
	cRolled, cErr := run(false)
	rolled, err := run(true)
	if !cRolled || cErr != nil {
		t.Fatalf("control: rollback ran=%v ctx.Err=%v, want a live context", cRolled, cErr)
	}
	if !rolled {
		t.Fatal("rollback never ran after the failed deploy")
	}
	if err != nil {
		t.Fatalf("rollback ran on a cancelled context after the client disconnected: %v", err)
	}
}
