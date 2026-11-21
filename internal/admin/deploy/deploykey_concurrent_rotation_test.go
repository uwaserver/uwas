package deploy

// Regression test for F5302: two overlapping deploy-key rotations shared one
// staging file (id_ed25519.tmp), so one request installed the other's key and
// the other failed with 500. The caller that got 200 was handed a public key
// that did not match the live private key. Gated ordering: both requests
// generate their key before either installs it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/uwaserver/uwas/internal/apps"
)

// deployKeyRaceDeps gates the post-generation ValidateDeployConfig call (every
// request calls it twice: probe before generating, then after generating).
type deployKeyRaceDeps struct {
	mgr     *apps.Manager
	mu      sync.Mutex
	calls   int
	arrived map[int]chan struct{}
	release map[int]chan struct{}
}

func (d *deployKeyRaceDeps) RequireAdmin(http.ResponseWriter, *http.Request) bool    { return true }
func (d *deployKeyRaceDeps) RequirePin(http.ResponseWriter, *http.Request) bool      { return true }
func (d *deployKeyRaceDeps) LogInfo(string, ...any)                                  {}
func (d *deployKeyRaceDeps) LogWarn(string, ...any)                                  {}
func (d *deployKeyRaceDeps) LogError(string, ...any)                                 {}
func (d *deployKeyRaceDeps) RecordAudit(*http.Request, string, string, bool)         {}
func (d *deployKeyRaceDeps) AppsManager() *apps.Manager                              { return d.mgr }
func (d *deployKeyRaceDeps) ConfigPath() string                                      { return "" }
func (d *deployKeyRaceDeps) Reload() error                                           { return nil }
func (d *deployKeyRaceDeps) AppCompleteDeploy(string, *apps.App, bool, string) error { return nil }
func (d *deployKeyRaceDeps) AppRollback(context.Context, string, *apps.App, string, apps.DeployConfig, map[string]string, bool, LogSink) (bool, string, string) {
	return false, "", ""
}
func (d *deployKeyRaceDeps) ValidateDeployConfig(*apps.App) error {
	d.mu.Lock()
	d.calls++
	n := d.calls
	arr, rel := d.arrived[n], d.release[n]
	d.mu.Unlock()
	if arr != nil {
		close(arr)
		<-rel
	}
	return nil
}

func deployKeyRaceCall(h *Handler) (int, AppDeployKeyResponse) {
	req := httptest.NewRequest("POST", "/api/v1/apps/keyed/deploy-key", nil)
	req.SetPathValue("name", "keyed")
	rec := httptest.NewRecorder()
	h.GenerateDeployKey(rec, req)
	var resp AppDeployKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func deployKeyRaceLivePub(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read live key: %v", err)
	}
	sg, err := ssh.ParsePrivateKey(b)
	if err != nil {
		t.Fatalf("parse live key: %v", err)
	}
	return string(ssh.MarshalAuthorizedKey(sg.PublicKey()))
}

func deployKeyRaceSetup(t *testing.T, gates ...int) (*Handler, *deployKeyRaceDeps, string) {
	dir := filepath.Join(t.TempDir(), "apps.d")
	store := apps.NewStore(dir)
	mgr := apps.NewManager(store, nil)
	if err := mgr.Register(&apps.App{Name: "keyed", Runtime: apps.RuntimeNode, WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	d := &deployKeyRaceDeps{mgr: mgr, arrived: map[int]chan struct{}{}, release: map[int]chan struct{}{}}
	for _, g := range gates {
		d.arrived[g] = make(chan struct{})
		d.release[g] = make(chan struct{})
	}
	return New(d), d, filepath.Join(dir, "deploy-keys", "keyed", "id_ed25519")
}

func deployKeyRaceWait(t *testing.T, ch chan struct{}, what string) {
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("invalid proof: timed out waiting for %s", what)
	}
}

func TestConcurrentDeployKeyRotationsStayConsistent(t *testing.T) {
	// Control: one request, live key matches the returned public key.
	h, _, keyPath := deployKeyRaceSetup(t)
	if code, resp := deployKeyRaceCall(h); code != 200 || deployKeyRaceLivePub(t, keyPath) != resp.PublicKey {
		t.Fatalf("control: code=%d", code)
	}

	h, d, keyPath := deployKeyRaceSetup(t, 2, 4)
	type res struct {
		code int
		resp AppDeployKeyResponse
	}
	ac, bc := make(chan res, 1), make(chan res, 1)
	go func() { c, r := deployKeyRaceCall(h); ac <- res{c, r} }()
	deployKeyRaceWait(t, d.arrived[2], "request A at gate")
	go func() { c, r := deployKeyRaceCall(h); bc <- res{c, r} }()
	deployKeyRaceWait(t, d.arrived[4], "request B at gate")
	close(d.release[2])
	a := <-ac
	close(d.release[4])
	b := <-bc

	if a.code != 200 || b.code != 200 {
		t.Errorf("rotations: A=%d B=%d, want both 200", a.code, b.code)
	}
	if live := deployKeyRaceLivePub(t, keyPath); live != b.resp.PublicKey {
		t.Errorf("live key does not match the last successful rotation's public key")
	}
	entries, _ := os.ReadDir(filepath.Dir(keyPath))
	for _, e := range entries {
		if e.Name() != "id_ed25519" {
			t.Errorf("leftover staging file %q", e.Name())
		}
	}
}
