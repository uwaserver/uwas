package admin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/logger"
)

// grpCManager builds a real apps.Manager rooted at a t.TempDir() so the
// store writes never touch the real /etc/uwas/apps.d. The manager is
// never asked to Start() a process in these tests (apps are created with
// start=false or Disabled), so no child process is ever spawned.
func grpCManager(t *testing.T) *apps.Manager {
	t.Helper()
	dir := t.TempDir()
	store := apps.NewStore(dir)
	store.DataRoot = filepath.Join(dir, "data")
	return apps.NewManager(store, logger.New("error", "text"))
}

// grpCServerWithApps returns a testServer wired with a real apps.Manager.
func grpCServerWithApps(t *testing.T) *Server {
	t.Helper()
	s := testServer()
	s.appsMgr = grpCManager(t)
	return s
}

// grpCSeedApp persists an app definition directly via the store so
// handlers operating on an existing app have something to read. The app
// is Disabled so nothing tries to launch it.
func grpCSeedApp(t *testing.T, s *Server, a *apps.App) *apps.App {
	t.Helper()
	if a.WorkDir == "" {
		a.WorkDir = s.appsMgr.Store().DefaultWorkDir(a.Name)
	}
	if err := s.appsMgr.Store().Save(a); err != nil {
		t.Fatalf("seed app %s: %v", a.Name, err)
	}
	def, err := s.appsMgr.Store().Get(a.Name)
	if err != nil {
		t.Fatalf("reload seeded app %s: %v", a.Name, err)
	}
	return def
}

// grpCDo invokes a handler directly with an admin context and returns the
// recorder.
func grpCDo(handler http.HandlerFunc, method, target string, body []byte) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, target, rdr)
	r = withAdminContext(r)
	rec := httptest.NewRecorder()
	handler(rec, r)
	return rec
}

// grpCDoNamed is like grpCDo but sets a PathValue("name") so handlers that
// read r.PathValue("name") see it without the ServeMux.
func grpCDoNamed(handler http.HandlerFunc, method, target, name string, body []byte) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, target, rdr)
	r.SetPathValue("name", name)
	r = withAdminContext(r)
	rec := httptest.NewRecorder()
	handler(rec, r)
	return rec
}

// -----------------------------------------------------------------------
// NotImplemented branches: appsMgr == nil for every apps handler.
// -----------------------------------------------------------------------

func TestGrpC_AppsHandlersNotEnabled(t *testing.T) {
	s := testServer() // appsMgr is nil
	if s.appsMgr != nil {
		t.Fatal("expected nil appsMgr from testServer")
	}

	// handleAppsList returns an empty list (200), not NotImplemented.
	rec := grpCDo(s.handleAppsList, "GET", "/api/v1/apps", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleAppsList nil mgr: status = %d, want 200", rec.Code)
	}

	type tc struct {
		name    string
		handler http.HandlerFunc
		method  string
	}
	cases := []tc{
		{"get", s.handleAppGet, "GET"},
		{"create", s.handleAppCreate, "POST"},
		{"update", s.handleAppUpdate, "PUT"},
		{"delete", s.handleAppDelete, "DELETE"},
		{"start", s.handleAppStart, "POST"},
		{"stop", s.handleAppStop, "POST"},
		{"restart", s.handleAppRestart, "POST"},
		{"stats", s.handleAppStats, "GET"},
		{"logs", s.handleAppLogs, "GET"},
		{"deploy", s.handleAppDeploy, "POST"},
		{"preflight", s.handleAppDeployPreflight, "POST"},
		{"deploykey", s.handleAppGenerateDeployKey, "POST"},
	}
	for _, c := range cases {
		rec := grpCDoNamed(c.handler, c.method, "/api/v1/apps/foo", "foo", []byte(`{}`))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s nil mgr: status = %d, want 501; body=%s", c.name, rec.Code, rec.Body.String())
		}
	}

	// Webhook handler also short-circuits on nil mgr (HMAC-gated, no admin
	// cookie required, so call it without an admin context).
	r := httptest.NewRequest("POST", "/api/v1/apps/foo/webhook", bytes.NewReader([]byte(`{}`)))
	r.SetPathValue("name", "foo")
	rec = httptest.NewRecorder()
	s.handleAppWebhook(rec, r)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("webhook nil mgr: status = %d, want 501", rec.Code)
	}
}

// -----------------------------------------------------------------------
// handleAppsList with a real manager and pagination.
// -----------------------------------------------------------------------

func TestGrpC_AppsListWithManager(t *testing.T) {
	s := grpCServerWithApps(t)
	grpCSeedApp(t, s, &apps.App{Name: "alpha", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true})
	// Register so it shows in Instances().
	def, _ := s.appsMgr.Store().Get("alpha")
	if err := s.appsMgr.Register(def); err != nil {
		t.Fatalf("register: %v", err)
	}

	rec := grpCDo(s.handleAppsList, "GET", "/api/v1/apps?limit=10&offset=0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("total = %d, want 1", resp.Total)
	}
}

// -----------------------------------------------------------------------
// handleAppGet: not-found, invalid name, success.
// -----------------------------------------------------------------------

func TestGrpC_AppGetBranches(t *testing.T) {
	s := grpCServerWithApps(t)

	// Not found.
	rec := grpCDoNamed(s.handleAppGet, "GET", "/api/v1/apps/missing", "missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing: status = %d, want 404", rec.Code)
	}

	// Invalid name → Store().Get returns error → 400.
	rec = grpCDoNamed(s.handleAppGet, "GET", "/api/v1/apps/bad..name", "bad..name", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid name: status = %d, want 400", rec.Code)
	}

	// Success — GitToken is redacted in the response.
	grpCSeedApp(t, s, &apps.App{
		Name:     "getme",
		Runtime:  apps.RuntimeCustom,
		Command:  "true",
		Disabled: true,
		Deploy:   apps.DeployConfig{GitURL: "https://github.com/x/y.git", GitToken: "secret-token"},
	})
	rec = grpCDoNamed(s.handleAppGet, "GET", "/api/v1/apps/getme", "getme", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get success: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatal("git token leaked in handleAppGet response")
	}
}

// -----------------------------------------------------------------------
// handleAppCreate: invalid JSON, conflict, validation failure, success
// with start=false (no process spawned).
// -----------------------------------------------------------------------

func TestGrpC_AppCreateBranches(t *testing.T) {
	s := grpCServerWithApps(t)

	// Invalid JSON.
	rec := grpCDo(s.handleAppCreate, "POST", "/api/v1/apps", []byte(`{not json`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: status = %d, want 400", rec.Code)
	}

	// Validation failure: unknown runtime.
	rec = grpCDo(s.handleAppCreate, "POST", "/api/v1/apps",
		[]byte(`{"name":"badrt","runtime":"frobnicate"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad runtime: status = %d, want 400", rec.Code)
	}

	// Success with start=false — definition persisted, never started.
	body := []byte(`{"name":"create-ok","runtime":"custom","command":"true","disabled":true}`)
	rec = grpCDo(s.handleAppCreate, "POST", "/api/v1/apps?start=false", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ok: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var cr map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cr["started"] != false {
		t.Fatalf("started = %v, want false", cr["started"])
	}
	if !s.appsMgr.Store().Exists("create-ok") {
		t.Fatal("app not persisted")
	}

	// Conflict: creating the same name again.
	rec = grpCDo(s.handleAppCreate, "POST", "/api/v1/apps?start=false", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict: status = %d, want 409", rec.Code)
	}
}

// -----------------------------------------------------------------------
// handleAppUpdate: not-found, invalid JSON, rename rejected, deploy-only
// patch (no restart path), success on a disabled app.
// -----------------------------------------------------------------------

func TestGrpC_AppUpdateBranches(t *testing.T) {
	s := grpCServerWithApps(t)

	// Not found.
	rec := grpCDoNamed(s.handleAppUpdate, "PUT", "/api/v1/apps/nope", "nope", []byte(`{}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: status = %d, want 404", rec.Code)
	}

	grpCSeedApp(t, s, &apps.App{Name: "upd", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true})

	// Invalid JSON.
	rec = grpCDoNamed(s.handleAppUpdate, "PUT", "/api/v1/apps/upd", "upd", []byte(`{bad`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("update bad json: status = %d, want 400", rec.Code)
	}

	// Rename via PUT rejected.
	rec = grpCDoNamed(s.handleAppUpdate, "PUT", "/api/v1/apps/upd", "upd", []byte(`{"name":"other"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rename: status = %d, want 400", rec.Code)
	}

	// Reserved env var rejected.
	rec = grpCDoNamed(s.handleAppUpdate, "PUT", "/api/v1/apps/upd", "upd",
		[]byte(`{"env":{"PATH":"/evil"}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reserved env: status = %d, want 400", rec.Code)
	}

	// Deploy-only patch (no operational fields) → saved, no restart attempt.
	rec = grpCDoNamed(s.handleAppUpdate, "PUT", "/api/v1/apps/upd", "upd",
		[]byte(`{"deploy":{"git_url":"https://github.com/x/y.git"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy-only patch: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	def, _ := s.appsMgr.Store().Get("upd")
	if def.Deploy.GitURL != "https://github.com/x/y.git" {
		t.Fatalf("deploy git_url not persisted: %q", def.Deploy.GitURL)
	}

	// Operational patch on a disabled app: started=false with disabled hint.
	rec = grpCDoNamed(s.handleAppUpdate, "PUT", "/api/v1/apps/upd", "upd",
		[]byte(`{"description":"hello","disabled":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("op patch: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var ur map[string]any
	json.Unmarshal(rec.Body.Bytes(), &ur)
	if ur["started"] != false {
		t.Fatalf("disabled update started = %v, want false", ur["started"])
	}
}

// -----------------------------------------------------------------------
// handleAppDelete: not-found, success.
// -----------------------------------------------------------------------

func TestGrpC_AppDeleteBranches(t *testing.T) {
	s := grpCServerWithApps(t)

	rec := grpCDoNamed(s.handleAppDelete, "DELETE", "/api/v1/apps/ghost", "ghost", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing: status = %d, want 404", rec.Code)
	}

	grpCSeedApp(t, s, &apps.App{Name: "delme", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true})
	rec = grpCDoNamed(s.handleAppDelete, "DELETE", "/api/v1/apps/delme", "delme", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if s.appsMgr.Store().Exists("delme") {
		t.Fatal("app still exists after delete")
	}
}

// -----------------------------------------------------------------------
// handleAppStart / Restart: not-found branches (no real process spawn).
// -----------------------------------------------------------------------

func TestGrpC_AppStartRestartNotFound(t *testing.T) {
	s := grpCServerWithApps(t)

	rec := grpCDoNamed(s.handleAppStart, "POST", "/api/v1/apps/x/start", "x", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("start missing: status = %d, want 404", rec.Code)
	}
	rec = grpCDoNamed(s.handleAppRestart, "POST", "/api/v1/apps/x/restart", "x", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("restart missing: status = %d, want 404", rec.Code)
	}
	// Invalid name → store error → 400.
	rec = grpCDoNamed(s.handleAppStart, "POST", "/api/v1/apps/bad..n/start", "bad..n", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start invalid name: status = %d, want 400", rec.Code)
	}
}

// -----------------------------------------------------------------------
// handleAppStop: not-found, and "already stopped" idempotent branch.
// -----------------------------------------------------------------------

func TestGrpC_AppStopBranches(t *testing.T) {
	s := grpCServerWithApps(t)

	rec := grpCDoNamed(s.handleAppStop, "POST", "/api/v1/apps/none/stop", "none", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stop missing: status = %d, want 404", rec.Code)
	}

	// Registered but never started → Stop returns "not running" → 200 idempotent.
	grpCSeedApp(t, s, &apps.App{Name: "stopme", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true})
	def, _ := s.appsMgr.Store().Get("stopme")
	_ = s.appsMgr.Register(def)
	rec = grpCDoNamed(s.handleAppStop, "POST", "/api/v1/apps/stopme/stop", "stopme", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop idempotent: status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------
// handleAppStats: not found (nil stats).
// -----------------------------------------------------------------------

func TestGrpC_AppStatsNotFound(t *testing.T) {
	s := grpCServerWithApps(t)
	rec := grpCDoNamed(s.handleAppStats, "GET", "/api/v1/apps/zzz/stats", "zzz", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stats missing: status = %d, want 404", rec.Code)
	}
}

// -----------------------------------------------------------------------
// handleAppLogs: not-found, empty runtime log, build-log fallback.
// -----------------------------------------------------------------------

func TestGrpC_AppLogsBranches(t *testing.T) {
	s := grpCServerWithApps(t)

	// Not found.
	rec := grpCDoNamed(s.handleAppLogs, "GET", "/api/v1/apps/nolog/logs", "nolog", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("logs missing: status = %d, want 404", rec.Code)
	}

	// App with a pinned workdir so we control the log directory layout.
	wd := filepath.Join(t.TempDir(), "app", "src")
	grpCSeedApp(t, s, &apps.App{
		Name:     "logged",
		Runtime:  apps.RuntimeCustom,
		Command:  "true",
		WorkDir:  wd,
		Disabled: true,
	})

	// No log file yet → empty runtime log.
	rec = grpCDoNamed(s.handleAppLogs, "GET", "/api/v1/apps/logged/logs", "logged", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty log: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var lr map[string]string
	json.Unmarshal(rec.Body.Bytes(), &lr)
	if lr["kind"] != "runtime" || lr["log"] != "" {
		t.Fatalf("empty runtime log: got %+v", lr)
	}

	// Build-log fallback: runtime log absent but build log present.
	logsDir := filepath.Join(filepath.Dir(wd), "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "logged-build.log"), []byte("build output here"), 0644); err != nil {
		t.Fatal(err)
	}
	rec = grpCDoNamed(s.handleAppLogs, "GET", "/api/v1/apps/logged/logs", "logged", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("build log: status = %d", rec.Code)
	}
	json.Unmarshal(rec.Body.Bytes(), &lr)
	if lr["kind"] != "build" || !strings.Contains(lr["log"], "build output here") {
		t.Fatalf("build log fallback: got %+v", lr)
	}

	// Runtime log present → returned in preference to build log.
	if err := os.WriteFile(filepath.Join(logsDir, "logged.log"), []byte("runtime line"), 0644); err != nil {
		t.Fatal(err)
	}
	rec = grpCDoNamed(s.handleAppLogs, "GET", "/api/v1/apps/logged/logs", "logged", nil)
	json.Unmarshal(rec.Body.Bytes(), &lr)
	if lr["kind"] != "runtime" || !strings.Contains(lr["log"], "runtime line") {
		t.Fatalf("runtime log preferred: got %+v", lr)
	}
}

// -----------------------------------------------------------------------
// handleAppDeployPreflight: not-found, and a successful preflight against
// a real app definition.
// -----------------------------------------------------------------------

func TestGrpC_AppDeployPreflightHandler(t *testing.T) {
	s := grpCServerWithApps(t)

	rec := grpCDoNamed(s.handleAppDeployPreflight, "POST", "/api/v1/apps/none/preflight", "none", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("preflight missing: status = %d, want 404", rec.Code)
	}

	grpCSeedApp(t, s, &apps.App{
		Name:     "pf",
		Runtime:  apps.RuntimeCustom,
		Command:  "true",
		Disabled: true,
	})
	rec = grpCDoNamed(s.handleAppDeployPreflight, "POST", "/api/v1/apps/pf/preflight", "pf", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight ok: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var pr AppDeployPreflightResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if len(pr.Checks) == 0 {
		t.Fatal("expected at least one preflight check")
	}
}

// -----------------------------------------------------------------------
// appDeployPreflight: pure-function branch coverage.
// -----------------------------------------------------------------------

func TestGrpC_AppDeployPreflightPure(t *testing.T) {
	// nil definition.
	checks := appDeployPreflight(nil)
	if len(checks) != 1 || checks[0].OK {
		t.Fatalf("nil def: %+v", checks)
	}

	// Empty workdir is flagged required+failing.
	checks = appDeployPreflight(&apps.App{Name: "x", Runtime: apps.RuntimeGo})
	found := false
	for _, c := range checks {
		if c.Name == "work_dir" && !c.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected failing work_dir check, got %+v", checks)
	}

	// Docker runtime requires the docker tool check.
	checks = appDeployPreflight(&apps.App{
		Name:    "dk",
		Runtime: apps.RuntimeDocker,
		WorkDir: t.TempDir(),
		Docker:  apps.DockerSpec{Image: "img", ContainerPort: 80, Build: apps.DockerBuild{Context: "."}},
	})
	hasDocker := false
	for _, c := range checks {
		if c.Name == "docker" {
			hasDocker = true
		}
	}
	if !hasDocker {
		t.Fatalf("expected docker tool check, got %+v", checks)
	}

	// Git URL present + bad branch → deploy_config check fails.
	checks = appDeployPreflight(&apps.App{
		Name:    "g",
		Runtime: apps.RuntimeCustom,
		WorkDir: t.TempDir(),
		Deploy:  apps.DeployConfig{GitURL: "https://github.com/x/y.git", GitBranch: "bad branch!"},
	})
	cfgFail := false
	for _, c := range checks {
		if c.Name == "deploy_config" && !c.OK {
			cfgFail = true
		}
	}
	if !cfgFail {
		t.Fatalf("expected failing deploy_config, got %+v", checks)
	}

	// SSH key path that doesn't exist → ssh_key check fails.
	checks = appDeployPreflight(&apps.App{
		Name:    "s",
		Runtime: apps.RuntimeCustom,
		WorkDir: t.TempDir(),
		Deploy:  apps.DeployConfig{SSHKeyPath: "/no/such/key/file"},
	})
	sshFail := false
	for _, c := range checks {
		if c.Name == "ssh_key" && !c.OK {
			sshFail = true
		}
	}
	if !sshFail {
		t.Fatalf("expected failing ssh_key, got %+v", checks)
	}
}

// -----------------------------------------------------------------------
// buildCommandTools: pure-function coverage.
// -----------------------------------------------------------------------

func TestGrpC_BuildCommandTools(t *testing.T) {
	got := buildCommandTools("npm ci && npm run build && corepack pnpm install")
	want := map[string]bool{"npm": true, "corepack": true}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected tool %q in %v", g, got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected deduped [npm corepack], got %v", got)
	}

	// Absolute-path binaries and empty segments are skipped.
	got = buildCommandTools("/usr/bin/make && && go build")
	for _, g := range got {
		if strings.Contains(g, "/") {
			t.Fatalf("absolute path leaked: %v", got)
		}
	}
	if len(got) != 1 || got[0] != "go" {
		t.Fatalf("expected [go], got %v", got)
	}

	// corepack with no subcommand falls through to generic handling.
	got = buildCommandTools("corepack")
	if len(got) != 1 || got[0] != "corepack" {
		t.Fatalf("bare corepack: got %v", got)
	}

	if len(buildCommandTools("")) != 0 {
		t.Fatal("empty command should yield no tools")
	}
}

// -----------------------------------------------------------------------
// validateDockerGitDeploy: pure-function coverage.
// -----------------------------------------------------------------------

func TestGrpC_ValidateDockerGitDeploy(t *testing.T) {
	// nil → ok.
	if err := validateDockerGitDeploy(nil); err != nil {
		t.Fatalf("nil: %v", err)
	}
	// Non-docker runtime → ok regardless of build context.
	if err := validateDockerGitDeploy(&apps.App{Runtime: apps.RuntimeNode}); err != nil {
		t.Fatalf("node: %v", err)
	}
	// Docker without build context → error.
	if err := validateDockerGitDeploy(&apps.App{Runtime: apps.RuntimeDocker}); err == nil {
		t.Fatal("docker without build context should error")
	}
	// Docker with build context → ok.
	err := validateDockerGitDeploy(&apps.App{
		Runtime: apps.RuntimeDocker,
		Docker:  apps.DockerSpec{Build: apps.DockerBuild{Context: "."}},
	})
	if err != nil {
		t.Fatalf("docker with context: %v", err)
	}
}

// -----------------------------------------------------------------------
// redactCommandArgs: pure-function coverage.
// -----------------------------------------------------------------------

func TestGrpC_RedactCommandArgs(t *testing.T) {
	out := redactCommandArgs([]string{
		"clone",
		"https://user:token@github.com/x/y.git",
		"https://github.com/x/y.git",
		"plain-arg",
	})
	if strings.Contains(out, "token") {
		t.Fatalf("token not redacted: %q", out)
	}
	// url.String() percent-encodes the redacted userinfo ("***" → "%2A%2A%2A").
	if !strings.Contains(out, "%2A%2A%2A") {
		t.Fatalf("expected redaction marker: %q", out)
	}
	if !strings.Contains(out, "https://github.com/x/y.git") {
		t.Fatalf("credential-free URL should be preserved: %q", out)
	}
	if !strings.Contains(out, "plain-arg") {
		t.Fatalf("plain arg dropped: %q", out)
	}
}

// -----------------------------------------------------------------------
// writeGitAskpass: writes an executable helper script with the token.
// -----------------------------------------------------------------------

func TestGrpC_WriteGitAskpass(t *testing.T) {
	// Redirect temp dir so the file lands under t.TempDir().
	t.Setenv("TMPDIR", t.TempDir())

	path, err := writeGitAskpass("s3cr3t'token")
	if err != nil {
		t.Fatalf("writeGitAskpass: %v", err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("perm = %o, want 0700", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(data)
	if !strings.HasPrefix(body, "#!/bin/sh") {
		t.Fatalf("missing shebang: %q", body)
	}
	if !strings.Contains(body, "x-access-token") {
		t.Fatal("askpass should emit x-access-token for the username prompt")
	}
	// The token is single-quote escaped inside the script.
	if !strings.Contains(body, "s3cr3t") {
		t.Fatalf("token not embedded: %q", body)
	}
}

// -----------------------------------------------------------------------
// generateAppDeployKey: pure-function (filesystem) coverage.
// -----------------------------------------------------------------------

func TestGrpC_GenerateAppDeployKey(t *testing.T) {
	// Empty store dir → error.
	if _, _, err := generateAppDeployKey("", "app"); err == nil {
		t.Fatal("empty store dir should error")
	}

	dir := t.TempDir()
	priv, pub, err := generateAppDeployKey(dir, "myapp")
	if err != nil {
		t.Fatalf("generateAppDeployKey: %v", err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Fatalf("unexpected public key format: %q", pub)
	}
	want := filepath.Join(dir, "deploy-keys", "myapp", "id_ed25519")
	if priv != want {
		t.Fatalf("private path = %q, want %q", priv, want)
	}
	info, err := os.Stat(priv)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private key perm = %o, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(priv)
	if !strings.Contains(string(data), "OPENSSH PRIVATE KEY") {
		t.Fatal("private key not in OpenSSH PEM format")
	}
}

// -----------------------------------------------------------------------
// handleAppGenerateDeployKey: not-found and success through the handler.
// -----------------------------------------------------------------------

func TestGrpC_AppGenerateDeployKeyHandler(t *testing.T) {
	s := grpCServerWithApps(t)

	rec := grpCDoNamed(s.handleAppGenerateDeployKey, "POST", "/api/v1/apps/none/deploy-key", "none", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deploy-key missing: status = %d, want 404", rec.Code)
	}

	grpCSeedApp(t, s, &apps.App{Name: "keyed", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true})
	rec = grpCDoNamed(s.handleAppGenerateDeployKey, "POST", "/api/v1/apps/keyed/deploy-key", "keyed", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy-key ok: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var kr AppDeployKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &kr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(kr.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("bad public key: %q", kr.PublicKey)
	}
	// SSHKeyPath should now be persisted on the app definition.
	def, _ := s.appsMgr.Store().Get("keyed")
	if def.Deploy.SSHKeyPath != kr.PrivateKeyPath {
		t.Fatalf("ssh key path not persisted: %q vs %q", def.Deploy.SSHKeyPath, kr.PrivateKeyPath)
	}
}

// -----------------------------------------------------------------------
// handleAppWebhook: not-found, secret-not-set, no-git-source, bad
// signature, branch-skip, and accepted (with branch filter).
// -----------------------------------------------------------------------

func grpCWebhookReq(name string, body []byte, secret string) *http.Request {
	r := httptest.NewRequest("POST", "/api/v1/apps/"+name+"/webhook", bytes.NewReader(body))
	r.SetPathValue("name", name)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return r
}

func TestGrpC_AppWebhookBranches(t *testing.T) {
	s := grpCServerWithApps(t)

	// Not found → 404.
	r := grpCWebhookReq("nope", []byte(`{}`), "")
	rec := httptest.NewRecorder()
	s.handleAppWebhook(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("webhook missing: status = %d, want 404", rec.Code)
	}

	// App with no webhook secret → 403.
	grpCSeedApp(t, s, &apps.App{Name: "nosecret", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true})
	r = grpCWebhookReq("nosecret", []byte(`{}`), "")
	rec = httptest.NewRecorder()
	s.handleAppWebhook(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no secret: status = %d, want 403", rec.Code)
	}

	// Secret set but no git source → 409.
	grpCSeedApp(t, s, &apps.App{
		Name: "nogit", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true,
		Deploy: apps.DeployConfig{WebhookSecret: "sekret"},
	})
	r = grpCWebhookReq("nogit", []byte(`{}`), "")
	rec = httptest.NewRecorder()
	s.handleAppWebhook(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("no git: status = %d, want 409", rec.Code)
	}

	// Fully configured app for signature tests.
	grpCSeedApp(t, s, &apps.App{
		Name: "hooked", Runtime: apps.RuntimeCustom, Command: "true", Disabled: true,
		Deploy: apps.DeployConfig{
			WebhookSecret: "topsecret",
			GitURL:        "https://github.com/x/y.git",
			BranchFilter:  "main",
		},
	})

	// Bad signature → 401.
	r = grpCWebhookReq("hooked", []byte(`{"ref":"refs/heads/main"}`), "")
	r.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec = httptest.NewRecorder()
	s.handleAppWebhook(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad sig: status = %d, want 401", rec.Code)
	}

	// Valid signature but branch doesn't match filter → 202 skipped (no
	// deploy goroutine launched).
	body := []byte(`{"ref":"refs/heads/develop"}`)
	r = grpCWebhookReq("hooked", body, "topsecret")
	rec = httptest.NewRecorder()
	s.handleAppWebhook(rec, r)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("branch skip: status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "skipped") {
		t.Fatalf("expected skipped reason, got %s", rec.Body.String())
	}
}

// -----------------------------------------------------------------------
// handleAppWebhookStatus: no-deploys-yet and after a recorded status.
// -----------------------------------------------------------------------

func TestGrpC_AppWebhookStatusHandler(t *testing.T) {
	s := grpCServerWithApps(t)

	rec := grpCDoNamed(s.handleAppWebhookStatus, "GET", "/api/v1/apps/fresh-grpc/webhook-status", "fresh-grpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("no deploys: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no webhook deploys yet") {
		t.Fatalf("expected no-deploys message, got %s", rec.Body.String())
	}

	// Record a status, then read it back.
	s.recordLastWebhook("recorded-grpc", &webhookDeployStatus{OK: true, CommitSHA: "abc123"})
	rec = grpCDoNamed(s.handleAppWebhookStatus, "GET", "/api/v1/apps/recorded-grpc/webhook-status", "recorded-grpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("recorded: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "abc123") {
		t.Fatalf("expected commit in status, got %s", rec.Body.String())
	}
}

// -----------------------------------------------------------------------
// handleAppDeployHistory: empty, in-memory, and on-disk load path.
// -----------------------------------------------------------------------

func TestGrpC_AppDeployHistoryHandler(t *testing.T) {
	s := grpCServerWithApps(t)

	// Empty in-memory and no on-disk file → empty items.
	rec := grpCDoNamed(s.handleAppDeployHistory, "GET", "/api/v1/apps/emptyhist-grpc/deploy-history", "emptyhist-grpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty history: status = %d", rec.Code)
	}

	// Record an entry in memory and via the Server (persists to disk too).
	s.recordAppDeployHistory("hist-grpc", appDeployHistoryEntry{Source: "manual", OK: true, CommitSHA: "sha-1"})
	rec = grpCDoNamed(s.handleAppDeployHistory, "GET", "/api/v1/apps/hist-grpc/deploy-history", "hist-grpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "sha-1") {
		t.Fatalf("expected recorded entry, got %s", rec.Body.String())
	}

	// On-disk load path: persist directly to the store dir, clear the
	// in-memory cache, then read through the handler.
	dir := s.appsMgr.Store().Dir
	entries := []appDeployHistoryEntry{{Source: "webhook", OK: true, CommitSHA: "disk-sha"}}
	if err := persistAppDeployHistory(dir, "ondisk-grpc", entries); err != nil {
		t.Fatalf("persist: %v", err)
	}
	deployHistoryMu.Lock()
	delete(deployHistory, "ondisk-grpc")
	deployHistoryMu.Unlock()
	rec = grpCDoNamed(s.handleAppDeployHistory, "GET", "/api/v1/apps/ondisk-grpc/deploy-history", "ondisk-grpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ondisk history: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "disk-sha") {
		t.Fatalf("expected on-disk entry loaded, got %s", rec.Body.String())
	}
}

// -----------------------------------------------------------------------
// persistAppDeployHistory: edge branches (empty root/name, trimming >20).
// -----------------------------------------------------------------------

func TestGrpC_PersistAppDeployHistory(t *testing.T) {
	// Empty root/name are no-ops returning nil.
	if err := persistAppDeployHistory("", "x", nil); err != nil {
		t.Fatalf("empty root: %v", err)
	}
	if err := persistAppDeployHistory(t.TempDir(), "", nil); err != nil {
		t.Fatalf("empty name: %v", err)
	}

	// More than 20 entries get trimmed to 20 on disk.
	dir := t.TempDir()
	var items []appDeployHistoryEntry
	for i := 0; i < 30; i++ {
		items = append(items, appDeployHistoryEntry{Source: "manual", CommitSHA: "c"})
	}
	if err := persistAppDeployHistory(dir, "trim-grpc", items); err != nil {
		t.Fatalf("persist: %v", err)
	}
	loaded := loadAppDeployHistory(dir, "trim-grpc")
	if len(loaded) != 20 {
		t.Fatalf("expected 20 trimmed entries, got %d", len(loaded))
	}

	// loadAppDeployHistory on a missing file → nil.
	if got := loadAppDeployHistory(dir, "absent-grpc"); got != nil {
		t.Fatalf("missing history should be nil, got %v", got)
	}
}

// -----------------------------------------------------------------------
// runWebhookDeploy: error path where the app disappears between webhook
// receipt and deploy. Records a failed history entry without spawning git.
// -----------------------------------------------------------------------

func TestGrpC_RunWebhookDeployAppDisappeared(t *testing.T) {
	s := grpCServerWithApps(t)
	// No app named "vanished" exists; runWebhookDeploy should record a
	// failure and return without panicking.
	s.runWebhookDeploy("vanished-grpc", "refs/heads/main")

	lastWebhookMu.Lock()
	st := lastWebhookByName["vanished-grpc"]
	lastWebhookMu.Unlock()
	if st == nil {
		t.Fatal("expected a recorded webhook status")
	}
	if st.OK {
		t.Fatal("expected failed status for vanished app")
	}
	if !strings.Contains(st.Error, "disappeared") {
		t.Fatalf("unexpected error: %q", st.Error)
	}
}

// -----------------------------------------------------------------------
// maybeReloadForApps: nil reloadFn (no-op) and an invoked reloadFn.
// -----------------------------------------------------------------------

func TestGrpC_MaybeReloadForApps(t *testing.T) {
	s := grpCServerWithApps(t)

	// nil reloadFn → no-op, no panic.
	s.reloadFn = nil
	s.maybeReloadForApps()

	// reloadFn set and returns nil.
	called := false
	s.reloadFn = func() error { called = true; return nil }
	s.maybeReloadForApps()
	if !called {
		t.Fatal("reloadFn not invoked")
	}

	// reloadFn returning an error is logged, not fatal.
	s.reloadFn = func() error { return context.DeadlineExceeded }
	s.maybeReloadForApps()
}
