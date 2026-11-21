package deploy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

// rollbackFlagDeps records the restart flag each rollback is asked for.
type rollbackFlagDeps struct {
	branchFilterDeps
	mu          sync.Mutex
	completeErr error
	restarts    []bool
}

func (d *rollbackFlagDeps) AppCompleteDeploy(string, *apps.App, bool, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.completeErr
}
func (d *rollbackFlagDeps) AppRollback(_ context.Context, _ string, _ *apps.App, sha string, _ apps.DeployConfig, _ map[string]string, restart bool, _ LogSink) (bool, string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restarts = append(d.restarts, restart)
	return true, sha, "rolled back"
}
func (d *rollbackFlagDeps) take() []bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.restarts
	d.restarts = nil
	return out
}

// TestManualDeployRollbackRestartFlag: a failed pull/build leaves the running
// app alone (restart=false, like the webhook path) (F65200); a post-deploy
// failure brings the app back up unless skip_start asked it to stay stopped
// (F65201).
func TestManualDeployRollbackRestartFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	deploy := func(h *Handler, body string) string {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/apps/demo/deploy", strings.NewReader(body))
		r.SetPathValue("name", "demo")
		rec := httptest.NewRecorder()
		h.Deploy(rec, r)
		return rec.Body.String()
	}
	cases := []struct {
		name        string
		completeErr error
		body        string
		want        bool
	}{
		{"build failure", nil, `{}`, false},
		{"build failure skip_start", nil, `{"skip_start":true}`, false},
		{"post-deploy failure", errors.New("not listening"), `{"build_cmd":"none"}`, true},
		{"post-deploy failure skip_start", errors.New("refresh failed"), `{"build_cmd":"none","skip_start":true}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serveURL, work := newServedRepo(t, "v1")
			store := apps.NewStore(t.TempDir())
			store.DataRoot = t.TempDir()
			// The stored build command fails unless the request overrides it.
			if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, Command: "true",
				Deploy: apps.DeployConfig{GitURL: serveURL, GitBranch: "main", BuildCmd: "false"}}); err != nil {
				t.Fatal(err)
			}
			d := &rollbackFlagDeps{branchFilterDeps: branchFilterDeps{mgr: apps.NewManager(store, nil)}}
			h := New(d)
			if resp := deploy(h, `{"build_cmd":"none"}`); !strings.Contains(resp, `"ok":true`) {
				t.Fatalf("initial deploy: %s", resp)
			}
			d.take()
			commitAndPush(t, work, "v2")

			d.mu.Lock()
			d.completeErr = tc.completeErr
			d.mu.Unlock()
			resp := deploy(h, tc.body)
			if !strings.Contains(resp, `"ok":false`) {
				t.Fatalf("deploy should fail: %s", resp)
			}
			if got := d.take(); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("rollback restart calls = %v, want [%v]", got, tc.want)
			}
		})
	}
}
