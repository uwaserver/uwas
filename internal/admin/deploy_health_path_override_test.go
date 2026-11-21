package admin

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// F66001 (regression): the dashboard deploy modal sends health_path with the
// other one-off overrides (git_url, git_branch, build_cmd). The server dropped
// it, so the post-deploy health check used the stored path (or none) and a
// deploy the operator asked to gate on a failing path was reported ok. The
// override must gate this deploy only and must not be persisted.
func TestManualDeployHealthPathOverride(t *testing.T) {
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend not found: %v", err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	root := t.TempDir()
	runGit(root, "init", "-q", "--bare", "-b", "main", filepath.Join(root, "repo.git"))
	gitSrv := httptest.NewTLSServer(&cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}})
	defer gitSrv.Close()
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "version.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(src, "add", "-A")
	runGit(src, "commit", "-q", "-m", "initial")
	runGit(src, "push", "-q", filepath.Join(root, "repo.git"), "main")

	health := http.NewServeMux()
	health.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	health.HandleFunc("/boom", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	healthSrv := httptest.NewServer(health)
	defer healthSrv.Close()
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(healthSrv.URL, "http://"))
	port, _ := strconv.Atoi(p)

	deploy := func(stored, body string) (bool, string) {
		t.Helper()
		store := apps.NewStore(t.TempDir())
		store.DataRoot = t.TempDir()
		if err := store.Save(&apps.App{
			Name: "demo", Runtime: apps.RuntimeCustom, Command: "sleep 999", Port: port,
			WorkDir: filepath.Join(t.TempDir(), "work"),
			Deploy:  apps.DeployConfig{GitURL: gitSrv.URL + "/repo.git", GitBranch: "main", BuildCmd: "none", HealthPath: stored},
		}); err != nil {
			t.Fatal(err)
		}
		mgr := apps.NewManager(store, logger.New("error", "text"))
		defer func() { _ = mgr.Stop("demo") }()
		s := New(&config.Config{Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}}}, logger.New("error", "text"), metrics.New())
		s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
		s.appsMgr = mgr
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apps/demo/deploy", strings.NewReader(body)))
		var res struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("deploy response %d: %s", rec.Code, rec.Body.String())
		}
		got, err := store.Get("demo")
		if err != nil || got == nil {
			t.Fatalf("reload app: %v", err)
		}
		return res.OK, got.Deploy.HealthPath
	}

	if ok, stored := deploy("", `{"health_path":"/boom"}`); ok || stored != "" {
		t.Errorf("failing health_path override: ok=%v stored=%q, want ok=false and nothing persisted", ok, stored)
	}
	if ok, stored := deploy("/boom", `{"health_path":"/ok"}`); !ok || stored != "/boom" {
		t.Errorf("passing override over failing stored path: ok=%v stored=%q, want ok=true and stored path kept", ok, stored)
	}
	if ok, _ := deploy("/boom", `{}`); ok {
		t.Error("control: stored failing health path without override must fail the deploy")
	}
}
