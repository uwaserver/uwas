package deploy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

// stoppedAppDeps records the skipStart and rollback restart flags of webhook
// deploys.
type stoppedAppDeps struct {
	branchFilterDeps
	mu          sync.Mutex
	completeErr error
	skipStarts  []bool
	restarts    []bool
}

func (d *stoppedAppDeps) AppCompleteDeploy(_ string, _ *apps.App, skipStart bool, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.skipStarts = append(d.skipStarts, skipStart)
	return d.completeErr
}
func (d *stoppedAppDeps) AppRollback(_ context.Context, _ string, _ *apps.App, sha string, _ apps.DeployConfig, _ map[string]string, restart bool, _ LogSink) (bool, string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restarts = append(d.restarts, restart)
	return true, sha, "rolled back"
}

// TestWebhookDeployKeepsStoppedAppStopped (F66501): an app the operator
// stopped (Disabled) stays stopped until the operator starts it; a git push
// deploys its code without starting it, and a failed push's rollback does not
// start it either. Enabled apps are still started.
func TestWebhookDeployKeepsStoppedAppStopped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cases := []struct {
		name         string
		disabled     bool
		completeErr  error
		wantSkip     bool
		wantRestarts []bool
	}{
		{"stopped app", true, nil, true, nil},
		{"enabled app", false, nil, false, nil},
		{"stopped app, post-deploy failure", true, errors.New("refresh failed"), true, []bool{false}},
		{"enabled app, post-deploy failure", false, errors.New("not listening"), false, []bool{true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serveURL, work := newServedRepo(t, "v1")
			store := apps.NewStore(t.TempDir())
			store.DataRoot = t.TempDir()
			if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, Command: "true", Disabled: tc.disabled,
				WorkDir: filepath.Join(t.TempDir(), "work"),
				Deploy:  apps.DeployConfig{GitURL: serveURL, GitBranch: "main", BuildCmd: "none", WebhookSecret: "s3cret-s3cret-s3cret"}}); err != nil {
				t.Fatal(err)
			}
			d := &stoppedAppDeps{branchFilterDeps: branchFilterDeps{mgr: apps.NewManager(store, nil)}}
			h := New(d)
			h.RunWebhookDeploy("demo", "refs/heads/main") // initial clone
			commitAndPush(t, work, "v2")
			d.mu.Lock()
			d.completeErr = tc.completeErr
			d.skipStarts, d.restarts = nil, nil
			d.mu.Unlock()

			h.RunWebhookDeploy("demo", "refs/heads/main")
			d.mu.Lock()
			defer d.mu.Unlock()
			if len(d.skipStarts) != 1 || d.skipStarts[0] != tc.wantSkip {
				t.Fatalf("AppCompleteDeploy skipStart = %v, want [%v]", d.skipStarts, tc.wantSkip)
			}
			if len(d.restarts) != len(tc.wantRestarts) || (len(d.restarts) == 1 && d.restarts[0] != tc.wantRestarts[0]) {
				t.Fatalf("rollback restart = %v, want %v", d.restarts, tc.wantRestarts)
			}
		})
	}
}

// TestProbeAppHealthDoesNotFollowRedirects (F66500): a 3xx passes the health
// check as documented; the probe must not chase the Location (an https
// upgrade on the plain app port, or a host the box cannot reach, failed a
// healthy deploy).
func TestProbeAppHealthDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()
	var self string
	mux := http.NewServeMux()
	mux.HandleFunc("/force-ssl", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+self+"/force-ssl", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/offbox", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/login", http.StatusFound)
	})
	mux.HandleFunc("/to-failing", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/x", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	self = strings.TrimPrefix(srv.URL, "http://")
	_, p, _ := net.SplitHostPort(self)
	port, _ := strconv.Atoi(p)
	def := &apps.App{Name: "demo", Port: port}

	for _, path := range []string{"/force-ssl", "/offbox", "/to-failing"} {
		if err := ProbeAppHealth(def, path); err != nil {
			t.Errorf("ProbeAppHealth(%s) = %v, want a 3xx to pass", path, err)
		}
	}
	if err := ProbeAppHealth(def, "/missing"); err == nil {
		t.Error("ProbeAppHealth(/missing) passed, want 404 to fail")
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("redirect target requested %d times, want 0", n)
	}
}
