package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// ──────────────────────────────────────────────
// validateAppEnvMap — reserved vars + name grammar
// ──────────────────────────────────────────────

func TestValidateAppEnvMap_Empty(t *testing.T) {
	if err := validateAppEnvMap(nil); err != nil {
		t.Errorf("nil map should be valid: %v", err)
	}
	if err := validateAppEnvMap(map[string]string{}); err != nil {
		t.Errorf("empty map should be valid: %v", err)
	}
}

func TestValidateAppEnvMap_ReservedVars(t *testing.T) {
	reserved := []string{"PATH", "LD_PRELOAD", "HOME", "USER", "SHELL", "IFS", "BASH_ENV"}
	for _, name := range reserved {
		env := map[string]string{name: "value"}
		err := validateAppEnvMap(env)
		if err == nil {
			t.Errorf("expected error for reserved env var %q", name)
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("error for %q should mention 'reserved', got: %v", name, err)
		}
	}
}

func TestValidateAppEnvMap_InvalidNames(t *testing.T) {
	bad := []string{"", "123startswithdigit", "has space", "has=equals", "has\nnewline"}
	for _, name := range bad {
		env := map[string]string{name: "value"}
		err := validateAppEnvMap(env)
		if err == nil {
			t.Errorf("expected error for invalid env name %q", name)
		}
	}
}

func TestValidateAppEnvMap_ValidNames(t *testing.T) {
	good := []string{"MY_VAR", "my_var", "_underscore", "FOO_BAR_123", "a", "Z", "_"}
	for _, name := range good {
		env := map[string]string{name: "value"}
		if err := validateAppEnvMap(env); err != nil {
			t.Errorf("unexpected error for valid name %q: %v", name, err)
		}
	}
}

func TestValidateAppEnvMap_MixedValidAndInvalid(t *testing.T) {
	env := map[string]string{
		"VALID_VAR": "ok",
		"PATH":      "override",
	}
	if err := validateAppEnvMap(env); err == nil {
		t.Error("expected error for map containing reserved var PATH")
	}
}

// ──────────────────────────────────────────────
// validateDeployConfig — deploy config validation
// ──────────────────────────────────────────────

func TestValidateDeployConfig_Nil(t *testing.T) {
	if err := validateDeployConfig(nil); err != nil {
		t.Errorf("nil app should be valid: %v", err)
	}
}

func TestValidateDeployConfig_Empty(t *testing.T) {
	app := &apps.App{}
	if err := validateDeployConfig(app); err != nil {
		t.Errorf("empty deploy config should be valid: %v", err)
	}
}

func TestValidateDeployConfig_Valid(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:    "https://github.com/user/repo.git",
			GitBranch: "main",
			BuildCmd:  "npm ci && npm run build",
		},
	}
	if err := validateDeployConfig(app); err != nil {
		t.Errorf("valid deploy config should pass: %v", err)
	}
}

func TestValidateDeployConfig_InvalidGitURL(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"ext::sh -c whoami",
		"http://github.com/user/repo.git",
	}
	for _, u := range cases {
		app := &apps.App{
			Deploy: apps.DeployConfig{GitURL: u},
		}
		if err := validateDeployConfig(app); err == nil {
			t.Errorf("expected error for git URL %q", u)
		}
	}
}

func TestValidateDeployConfig_InvalidGitBranch(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:    "https://github.com/user/repo.git",
			GitBranch: "main; rm -rf /",
		},
	}
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for malicious branch name")
	}
}

func TestValidateDeployConfig_InvalidBranchFilter(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:      "https://github.com/user/repo.git",
			BranchFilter: "main`whoami`",
		},
	}
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for malicious branch filter")
	}
}

func TestValidateDeployConfig_InvalidBuildCmd(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:   "https://github.com/user/repo.git",
			BuildCmd: "npm ci; rm -rf /",
		},
	}
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for malicious build command")
	}
}

func TestValidateDeployConfig_InvalidHealthPath(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:     "https://github.com/user/repo.git",
			HealthPath: "no-leading-slash",
		},
	}
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for health path without leading /")
	}
}

func TestValidateDeployConfig_GitTokenWithNonHTTPS(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:   "ssh://git@github.com/user/repo.git",
			GitToken: "ghp_abc123",
		},
	}
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for git_token with non-https URL")
	}
}

func TestValidateDeployConfig_GitTokenControlChars(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:   "https://github.com/user/repo.git",
			GitToken: "token\nwith_newline",
		},
	}
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for git_token with control characters")
	}
}

func TestValidateDeployConfig_SSHKeyPathInvalid(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:     "https://github.com/user/repo.git",
			SSHKeyPath: "relative/path/key",
		},
	}
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for relative SSH key path")
	}

	app.Deploy.SSHKeyPath = "/valid/abs/path\x00key"
	if err := validateDeployConfig(app); err == nil {
		t.Error("expected error for SSH key path with null byte")
	}
}

func TestValidateDeployConfig_GitTokenHTTPSOK(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			GitURL:   "https://github.com/user/repo.git",
			GitToken: "ghp_validToken123",
		},
	}
	if err := validateDeployConfig(app); err != nil {
		t.Errorf("https git_url + token should be valid: %v", err)
	}
}

func TestValidateDeployConfig_SSHKeyPathAbsOK(t *testing.T) {
	app := &apps.App{
		Deploy: apps.DeployConfig{
			SSHKeyPath: "/etc/uwas/keys/deploy_key",
		},
	}
	if err := validateDeployConfig(app); err != nil {
		t.Errorf("absolute SSH key path should be valid: %v", err)
	}
}

// ──────────────────────────────────────────────
// buildShellCmd — returns platform-appropriate cmd
// ──────────────────────────────────────────────

func TestBuildShellCmd(t *testing.T) {
	ctx := context.Background()
	cmd := buildShellCmd(ctx, "npm ci && npm run build")
	if cmd == nil {
		t.Fatal("buildShellCmd returned nil")
	}
	if runtime.GOOS == "windows" {
		if len(cmd.Args) < 3 || cmd.Args[0] != "cmd" || cmd.Args[1] != "/C" {
			t.Errorf("on windows, expected cmd /C <command>, got args: %v", cmd.Args)
		}
	} else {
		if len(cmd.Args) < 3 || cmd.Args[0] != "sh" || cmd.Args[1] != "-c" {
			t.Errorf("on unix, expected sh -c <command>, got args: %v", cmd.Args)
		}
	}
	lastArg := cmd.Args[len(cmd.Args)-1]
	if lastArg != "npm ci && npm run build" {
		t.Errorf("expected command 'npm ci && npm run build', got %q", lastArg)
	}
}

func TestBuildShellCmd_EmptyCommand(t *testing.T) {
	ctx := context.Background()
	cmd := buildShellCmd(ctx, "")
	if cmd == nil {
		t.Fatal("buildShellCmd returned nil")
	}
	lastArg := cmd.Args[len(cmd.Args)-1]
	if lastArg != "" {
		t.Errorf("expected empty command, got %q", lastArg)
	}
}

// ──────────────────────────────────────────────
// httpsGitURLToSSH — converts HTTPS to SSH format
// ──────────────────────────────────────────────

func TestHTTPSGitURLToSSH_KnownHosts(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"https://github.com/user/repo.git", "git@github.com:user/repo.git", true},
		{"https://github.com/user/repo", "git@github.com:user/repo", true},
		{"https://gitlab.com/group/project.git", "git@gitlab.com:group/project.git", true},
		{"https://bitbucket.org/team/repo.git", "git@bitbucket.org:team/repo.git", true},
	}
	for _, tc := range cases {
		got, ok := httpsGitURLToSSH(tc.input)
		if !ok {
			t.Errorf("httpsGitURLToSSH(%q) = (%q, false), want (%q, true)", tc.input, got, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("httpsGitURLToSSH(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHTTPSGitURLToSSH_UnknownHost(t *testing.T) {
	got, ok := httpsGitURLToSSH("https://gitlab.example.internal/team/project.git")
	if ok {
		t.Errorf("expected false for unknown host, got (true, %q)", got)
	}
}

func TestHTTPSGitURLToSSH_NoScheme(t *testing.T) {
	got, ok := httpsGitURLToSSH("ssh://git@github.com/user/repo.git")
	if ok {
		t.Errorf("expected false for non-https scheme, got (true, %q)", got)
	}
}

func TestHTTPSGitURLToSSH_Empty(t *testing.T) {
	got, ok := httpsGitURLToSSH("")
	if ok {
		t.Errorf("expected false for empty URL, got (true, %q)", got)
	}
}

func TestHTTPSGitURLToSSH_WithCredentials(t *testing.T) {
	got, ok := httpsGitURLToSSH("https://token@github.com/user/repo.git")
	if ok {
		t.Errorf("expected false for URL with user info, got (true, %q)", got)
	}
}

func TestHTTPSGitURLToSSH_WithQuery(t *testing.T) {
	got, ok := httpsGitURLToSSH("https://github.com/user/repo.git?shallow=1")
	if ok {
		t.Errorf("expected false for URL with query params, got (true, %q)", got)
	}
}

func TestHTTPSGitURLToSSH_WithFragment(t *testing.T) {
	got, ok := httpsGitURLToSSH("https://github.com/user/repo.git#readme")
	if ok {
		t.Errorf("expected false for URL with fragment, got (true, %q)", got)
	}
}

func TestHTTPSGitURLToSSH_Whitespace(t *testing.T) {
	got, ok := httpsGitURLToSSH("  https://github.com/user/repo.git  ")
	if !ok {
		t.Error("expected true for URL with surrounding whitespace")
	}
	expected := "git@github.com:user/repo.git"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// ──────────────────────────────────────────────
// gitAuthEnv — git authentication environment
// ──────────────────────────────────────────────

func TestGitAuthEnv_NoAuth(t *testing.T) {
	env, url, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "", "")
	if err != nil {
		t.Fatalf("gitAuthEnv with no auth should succeed: %v", err)
	}
	defer cleanup()
	if url != "https://github.com/user/repo.git" {
		t.Errorf("expected clone URL %q, got %q", "https://github.com/user/repo.git", url)
	}
	hasGitTermPrompt := false
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_TERMINAL_PROMPT=") {
			hasGitTermPrompt = true
			if !strings.Contains(e, "0") {
				t.Errorf("expected GIT_TERMINAL_PROMPT=0, got %q", e)
			}
		}
	}
	if !hasGitTermPrompt {
		t.Error("expected GIT_TERMINAL_PROMPT=0 in env")
	}
}

func TestGitAuthEnv_WithToken(t *testing.T) {
	env, url, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "", "ghp_test123")
	if err != nil {
		t.Fatalf("gitAuthEnv with token should succeed: %v", err)
	}
	defer cleanup()
	if url != "https://github.com/user/repo.git" {
		t.Errorf("expected clone URL %q, got %q", "https://github.com/user/repo.git", url)
	}
	hasAskPass := false
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_ASKPASS=") {
			hasAskPass = true
			path := strings.TrimPrefix(e, "GIT_ASKPASS=")
			if _, err := os.Stat(path); err != nil {
				t.Errorf("askpass file %s does not exist: %v", path, err)
			}
		}
	}
	if !hasAskPass {
		t.Error("expected GIT_ASKPASS in env when git_token is set")
	}
}

func TestGitAuthEnv_TokenWithNonHTTPS(t *testing.T) {
	_, _, cleanup, err := gitAuthEnv("ssh://git@github.com/user/repo.git", "", "token")
	defer cleanup()
	if err == nil {
		t.Error("expected error for git_token with non-https URL")
	}
}

func TestGitAuthEnv_TokenControlChars(t *testing.T) {
	_, _, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "", "token\x00null")
	defer cleanup()
	if err == nil {
		t.Error("expected error for git_token with null byte")
	}
}

func TestGitAuthEnv_SSHKeyNotFound(t *testing.T) {
	_, _, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "/nonexistent/path/key", "")
	defer cleanup()
	if err == nil {
		t.Error("expected error for non-existent SSH key path")
	}
}

func TestGitAuthEnv_SSHKeyExists(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "deploy_key")
	if err := os.WriteFile(keyPath, []byte("fake-key-data"), 0600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	env, url, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", keyPath, "")
	if err != nil {
		t.Fatalf("gitAuthEnv with valid SSH key should succeed: %v", err)
	}
	defer cleanup()
	hasSSHCommand := false
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			hasSSHCommand = true
			if !strings.Contains(e, keyPath) {
				t.Errorf("GIT_SSH_COMMAND should include key path %q, got %q", keyPath, e)
			}
		}
	}
	if !hasSSHCommand {
		t.Error("expected GIT_SSH_COMMAND in env when SSH key is set")
	}
	if url != "git@github.com:user/repo.git" {
		t.Errorf("expected SSH-style clone URL when SSH key is set, got %q", url)
	}
}

func TestGitAuthEnv_SSHKeyOwnHost(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "deploy_key")
	if err := os.WriteFile(keyPath, []byte("fake-key-data"), 0600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	env, url, cleanup, err := gitAuthEnv("https://gitlab.example.internal/team/project.git", keyPath, "")
	if err != nil {
		t.Fatalf("gitAuthEnv with valid SSH key and unknown host should succeed: %v", err)
	}
	defer cleanup()
	if url != "https://gitlab.example.internal/team/project.git" {
		t.Errorf("expected original HTTPS URL for unknown host, got %q", url)
	}
	hasSSHCommand := false
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			hasSSHCommand = true
			break
		}
	}
	if !hasSSHCommand {
		t.Error("expected GIT_SSH_COMMAND in env even for unknown hosts")
	}
}

func TestGitAuthEnv_SSHKeyRelativePath(t *testing.T) {
	_, _, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "relative/key", "")
	defer cleanup()
	if err == nil {
		t.Error("expected error for relative SSH key path")
	}
}

func TestGitAuthEnv_SSHKeyWithControlChars(t *testing.T) {
	_, _, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "/valid/path\x00key", "")
	defer cleanup()
	if err == nil {
		t.Error("expected error for SSH key path with control characters")
	}
}

func TestGitAuthEnv_TokenWithInvalidURL(t *testing.T) {
	_, _, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "", "token\nnewline")
	defer cleanup()
	if err == nil {
		t.Error("expected error for token with newline")
	}
}

func TestGitAuthEnv_SSHKeyWithTokenNoConversion(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "deploy_key")
	if err := os.WriteFile(keyPath, []byte("fake-key-data"), 0600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	env, url, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", keyPath, "ghp_token123")
	if err != nil {
		t.Fatalf("SSH key + token should succeed: %v", err)
	}
	defer cleanup()
	if !strings.HasPrefix(url, "https://") {
		t.Errorf("expected HTTPS URL when token is present, got %q", url)
	}
	hasSSHCommand := false
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			hasSSHCommand = true
			break
		}
	}
	if !hasSSHCommand {
		t.Error("expected GIT_SSH_COMMAND in env when SSH key is present even with token")
	}
}

// ──────────────────────────────────────────────
// handleAppDeploy — HTTP handler coverage
// ──────────────────────────────────────────────

func TestHandleAppDeploy_NoManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/demo-app/deploy", nil))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501 (not implemented)", rec.Code)
	}
}

func TestHandleAppDeploy_NotFound(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/nonexistent-app/deploy", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleAppDeploy_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	app := &apps.App{
		Name:     "testapp",
		Runtime:  apps.RuntimeCustom,
		WorkDir:  filepath.Join(dir, "apps", "testapp"),
	}
	if err := store.Save(app); err != nil {
		t.Fatalf("save app: %v", err)
	}
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/testapp/deploy",
		strings.NewReader(`{invalid json`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleAppDeploy_InvalidGitURL(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	app := &apps.App{
		Name:    "testapp",
		Runtime: apps.RuntimeCustom,
		WorkDir: filepath.Join(dir, "apps", "testapp"),
	}
	if err := store.Save(app); err != nil {
		t.Fatalf("save app: %v", err)
	}
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr

	body := `{"git_url": "file:///etc/passwd"}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/testapp/deploy",
		strings.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400, body=%s", rec.Code, truncateStr(rec.Body.String(), 80))
	}
}

func TestHandleAppDeploy_InvalidEnvVars(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	app := &apps.App{
		Name:    "testapp",
		Runtime: apps.RuntimeCustom,
		WorkDir: filepath.Join(dir, "apps", "testapp"),
		Deploy: apps.DeployConfig{
			GitURL: "https://github.com/user/repo.git",
		},
	}
	if err := store.Save(app); err != nil {
		t.Fatalf("save app: %v", err)
	}
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr

	body := `{"git_url": "https://github.com/user/repo.git", "env": {"PATH": "/override"}}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/testapp/deploy",
		strings.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (reserved env var), body=%s", rec.Code, truncateStr(rec.Body.String(), 80))
	}
}

func TestHandleAppDeploy_InvalidEnvName(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	app := &apps.App{
		Name:    "testapp",
		Runtime: apps.RuntimeCustom,
		WorkDir: filepath.Join(dir, "apps", "testapp"),
		Deploy: apps.DeployConfig{
			GitURL: "https://github.com/user/repo.git",
		},
	}
	if err := store.Save(app); err != nil {
		t.Fatalf("save app: %v", err)
	}
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr


	body := `{"git_url": "https://github.com/user/repo.git", "env": {"1INVALID": "value"}}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/testapp/deploy",
		strings.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (invalid env name), body=%s", rec.Code, truncateStr(rec.Body.String(), 80))
	}
}

func TestHandleAppDeploy_EmptyWorkDir(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	app := &apps.App{
		Name:    "testapp",
		Runtime: apps.RuntimeCustom,
		Deploy: apps.DeployConfig{
			GitURL: "https://github.com/user/repo.git",
		},
	}
	if err := store.Save(app); err != nil {
		t.Fatalf("save app: %v", err)
	}
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr


	body := `{"git_url": "https://github.com/user/repo.git"}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/testapp/deploy",
		strings.NewReader(body)))
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 (empty workdir), body=%s", rec.Code, truncateStr(rec.Body.String(), 80))
	}
}

func TestHandleAppDeploy_DockerNoBuildContext(t *testing.T) {
	// validateDockerGitDeploy is tested separately; here we verify the
	// handler rejects a docker app without a git URL (no-op deploy).
	dir := t.TempDir()
	store := apps.NewStore(dir)
	app := &apps.App{
		Name:    "dockertest",
		Runtime: apps.RuntimeDocker,
		WorkDir: filepath.Join(dir, "apps", "dockertest"),
		Docker:  apps.DockerSpec{Image: "nginx:alpine", ContainerPort: 80},
	}
	if err := store.Save(app); err != nil {
		t.Fatalf("save app: %v", err)
	}
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr

	body := `{"git_url": "https://github.com/user/repo.git"}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/dockertest/deploy",
		strings.NewReader(body)))
	// Docker app with Image (not Build.Context) should trigger validateDockerGitDeploy error
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (docker without build.context), body=%s", rec.Code, truncateStr(rec.Body.String(), 100))
	}
}

func TestValidateDockerGitDeploy_Direct(t *testing.T) {
	// Test the validateDockerGitDeploy function directly for edge cases.
	cases := []struct {
		def  *apps.App
		want string // expected error substring, empty for success
	}{
		{nil, ""},
		{&apps.App{Runtime: apps.RuntimeNode}, ""},
		{&apps.App{Runtime: apps.RuntimeDocker, Docker: apps.DockerSpec{Image: "nginx"}}, "docker git deploy requires docker.build.context"},
		{&apps.App{Runtime: apps.RuntimeDocker, Docker: apps.DockerSpec{Build: apps.DockerBuild{Context: "./context"}}}, ""},
	}
	for i, tc := range cases {
		err := validateDockerGitDeploy(tc.def)
		if tc.want == "" && err != nil {
			t.Errorf("case %d: unexpected error: %v", i, err)
		}
		if tc.want != "" && err == nil {
			t.Errorf("case %d: expected error containing %q, got nil", i, tc.want)
		}
		if tc.want != "" && err != nil && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("case %d: error %q does not contain %q", i, err.Error(), tc.want)
		}
	}
}
// ──────────────────────────────────────────────
// handleAppCreate endpoint-level env validation
// ──────────────────────────────────────────────

func TestHandleAppCreate_InvalidEnv(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr


	body := `{"name": "testapp", "env": {"PATH": "malicious"}}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps", strings.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (reserved env var in create), body=%s", rec.Code, truncateStr(rec.Body.String(), 80))
	}
}

// ──────────────────────────────────────────────
// handleAppUpdate endpoint-level env validation
// ──────────────────────────────────────────────

func TestHandleAppUpdate_InvalidEnv(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	app := &apps.App{
		Name:     "testapp",
		Runtime:  apps.RuntimeCustom,
		WorkDir:  filepath.Join(dir, "apps", "testapp"),
	}
	if err := store.Save(app); err != nil {
		t.Fatalf("save app: %v", err)
	}
	appMgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = appMgr


	body := `{"env": {"123badname": "value"}}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/apps/testapp", strings.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (invalid env name in update), body=%s", rec.Code, truncateStr(rec.Body.String(), 80))
	}
}

// ──────────────────────────────────────────────
// shellQuote helper
// ──────────────────────────────────────────────

func TestShellQuote(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"no'quote", "'no'\\''quote'"},
		{"a'b'c", "'a'\\''b'\\''c'"},
		{"path/to/file", "'path/to/file'"},
	}
	for _, tc := range cases {
		got := shellQuote(tc.input)
		if got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ──────────────────────────────────────────────
// registerDashboardUI — dashboard routes
// ──────────────────────────────────────────────

func TestRegisterDashboardUI_ServesRoot(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/_uwas/dashboard/", nil))
	if rec.Code != 200 && rec.Code != 500 {
		t.Errorf("status = %d, want 200 or 500", rec.Code)
	}
}

func TestRegisterDashboardUI_ServesAssets(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/_uwas/dashboard/index.html", nil))
	// May return 200 (file served), 301 (redirect), or 500 (no embedded FS)
	if rec.Code != 200 && rec.Code != 301 && rec.Code != 500 {
		t.Errorf("status = %d, want 200 or 301 or 500", rec.Code)
	}
}

func TestRegisterDashboardUI_SecurityHeaders(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/_uwas/dashboard/", nil))
	if rec.Code != 200 && rec.Code != 500 {
		return // skip header check if not serving
	}
	h := rec.Header()
	if h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", h.Get("X-Frame-Options"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", h.Get("X-Content-Type-Options"))
	}
	if h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", h.Get("Referrer-Policy"))
	}
	if h.Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy header is empty")
	}
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
