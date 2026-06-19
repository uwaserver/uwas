package deploy

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// ---------------------------------------------------------------------------
// safeGitURL: non-whitelisted scheme rejection
// ---------------------------------------------------------------------------

func TestSafeGitURL_NonWhitelistedScheme(t *testing.T) {
	tests := []string{
		"http://github.com/user/repo.git", // http not in whitelist
		"ftp://example.com/repo.git",
		"justsometext",
		"javascript:alert(1)",
	}
	for _, u := range tests {
		err := safeGitURL(u)
		if err == nil {
			t.Errorf("safeGitURL(%q) expected error for non-whitelisted scheme", u)
			continue
		}
		if !strings.Contains(err.Error(), "only https://, ssh://, and git@") {
			t.Errorf("safeGitURL(%q) error = %v, want whitelist error", u, err)
		}
	}
}

func TestSafeGitURL_SSHScheme(t *testing.T) {
	if err := safeGitURL("ssh://git@github.com/user/repo.git"); err != nil {
		t.Errorf("safeGitURL ssh:// unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// validateShellCommand: full branch coverage
// ---------------------------------------------------------------------------

func TestValidateShellCommand_OK(t *testing.T) {
	if err := validateShellCommand("npm install"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateShellCommand_ControlChars(t *testing.T) {
	for _, c := range []string{"echo\nfoo", "echo\rfoo", "echo\x00foo"} {
		err := validateShellCommand(c)
		if err == nil {
			t.Errorf("validateShellCommand(%q) expected error", c)
			continue
		}
		if !strings.Contains(err.Error(), "forbidden control characters") {
			t.Errorf("validateShellCommand(%q) error = %v, want control char error", c, err)
		}
	}
}

func TestValidateShellCommand_Metacharacters(t *testing.T) {
	tests := []struct {
		cmd  string
		meta string
	}{
		{"echo $(whoami)", "$("},
		{"echo `whoami`", "`"},
		{"cat a | grep b", "|"},
		{"echo hi > file", ">"},
		{"cat < file", "<"},
		{"echo a; echo b", ";"},
		{"a && b", "&&"},
		// "||" contains "|" which is checked first, so the reported metachar is "|".
		{"a || b", "|"},
	}
	for _, tt := range tests {
		err := validateShellCommand(tt.cmd)
		if err == nil {
			t.Errorf("validateShellCommand(%q) expected error", tt.cmd)
			continue
		}
		if !strings.Contains(err.Error(), tt.meta) {
			t.Errorf("validateShellCommand(%q) error = %v, want metachar %q", tt.cmd, err, tt.meta)
		}
	}
}

// runShell goes through validateShellCommand; verify metachar rejection surfaces.
func TestRunShell_RejectsMetacharacter(t *testing.T) {
	_, err := runShell("", nil, "echo a; rm -rf /")
	if err == nil {
		t.Fatal("expected error for command with metacharacter")
	}
	if !strings.Contains(err.Error(), "forbidden shell metacharacter") {
		t.Errorf("error = %v, want metacharacter error", err)
	}
}

func TestRunShell_RejectsControlChar(t *testing.T) {
	_, err := runShell("", nil, "echo a\nrm -rf /")
	if err == nil {
		t.Fatal("expected error for command with newline")
	}
}

// ---------------------------------------------------------------------------
// waitForAppImpl: real implementation (success + timeout)
// ---------------------------------------------------------------------------

func TestWaitForAppImpl_Success(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	if err := waitForAppImpl(ln.Addr().String(), 2*time.Second); err != nil {
		t.Errorf("waitForAppImpl() error = %v, want nil", err)
	}
}

func TestWaitForAppImpl_Timeout(t *testing.T) {
	// Pick an address that is almost certainly not listening.
	// Reserve a port then close the listener so nothing is bound.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	start := time.Now()
	err = waitForAppImpl(addr, 300*time.Millisecond)
	if err == nil {
		t.Error("waitForAppImpl() expected timeout error")
	}
	if err != nil && !strings.Contains(err.Error(), "not responding") {
		t.Errorf("error = %v, want 'not responding'", err)
	}
	// Sanity: it should have respected the deadline roughly.
	if time.Since(start) > 5*time.Second {
		t.Error("waitForAppImpl took far longer than timeout")
	}
}

// ---------------------------------------------------------------------------
// CancelDeploy: all branches
// ---------------------------------------------------------------------------

func TestCancelDeploy_NoDeploy(t *testing.T) {
	m := New(nil)
	if m.CancelDeploy("missing.com") {
		t.Error("CancelDeploy() = true, want false for unknown domain")
	}
}

func TestCancelDeploy_Success(t *testing.T) {
	m := New(nil)
	m.cancelCh["test.com"] = make(chan struct{})
	if !m.CancelDeploy("test.com") {
		t.Error("CancelDeploy() = false, want true")
	}
	// After cancel, the channel is deleted -> second call returns false.
	if m.CancelDeploy("test.com") {
		t.Error("second CancelDeploy() = true, want false")
	}
}

func TestCancelDeploy_AlreadyClosed(t *testing.T) {
	m := New(nil)
	ch := make(chan struct{})
	close(ch)
	m.cancelCh["test.com"] = ch
	// Channel present but already closed -> returns false.
	if m.CancelDeploy("test.com") {
		t.Error("CancelDeploy() = true, want false for already-closed channel")
	}
}

// ---------------------------------------------------------------------------
// deployGit: cancellation branch + health check branches
// ---------------------------------------------------------------------------

func TestDeployGit_Cancelled(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "git clone") {
			os.MkdirAll(gitDir, 0755)
			return "cloned", nil
		}
		if strings.Contains(cmd, "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	m := New(nil)
	// Pre-create a cancel channel and close it so the select hits the cancel case.
	ch := make(chan struct{})
	close(ch)

	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder
	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "npm install",
	}

	err := m.deployGit(req, appRoot, "main", ch, status, &log)
	if err == nil {
		t.Fatal("deployGit() expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %v, want 'cancelled'", err)
	}
}

func TestDeployGit_SkipBuildKeyword(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}
	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		t.Error("runShell should not be called when build is 'skip'")
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder

	for _, kw := range []string{"skip", "none"} {
		log.Reset()
		req := DeployRequest{
			Domain:    "test.com",
			GitURL:    "https://github.com/user/repo.git",
			GitBranch: "main",
			BuildCmd:  kw,
		}
		if err := m.deployGit(req, appRoot, "main", nil, status, &log); err != nil {
			t.Errorf("deployGit() with BuildCmd=%q error = %v", kw, err)
		}
	}
}

func TestDeployGit_HealthCheckSuccess(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}
	var checkedAddr string
	waitForAppFn = func(addr string, timeout time.Duration) error {
		checkedAddr = addr
		return nil
	}

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now(), AppPort: 8081}
	var log strings.Builder
	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "none",
	}
	if err := m.deployGit(req, appRoot, "main", nil, status, &log); err != nil {
		t.Errorf("deployGit() error = %v", err)
	}
	if checkedAddr != "127.0.0.1:8081" {
		t.Errorf("health check addr = %q, want 127.0.0.1:8081", checkedAddr)
	}
	if !strings.Contains(log.String(), "App is healthy") {
		t.Error("expected 'App is healthy' in log")
	}
}

func TestDeployGit_HealthCheckFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}
	waitForAppFn = func(addr string, timeout time.Duration) error {
		return fmt.Errorf("connection refused")
	}

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now(), AppPort: 8082}
	var log strings.Builder
	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "none",
	}
	err := m.deployGit(req, appRoot, "main", nil, status, &log)
	if err == nil {
		t.Fatal("deployGit() expected health check error")
	}
	if !strings.Contains(err.Error(), "app health check failed") {
		t.Errorf("error = %v, want 'app health check failed'", err)
	}
}

// Token on an existing repo exercises the `git remote set-url` branch and detectBuildCmd default.
func TestDeployGit_ExistingRepoTokenRemoteSetURL(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)
	// No package.json/etc -> detectBuildCmd returns "" so no build runs.

	var sawSetURL bool
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "remote set-url") {
			sawSetURL = true
			// token must be present in the rewritten URL
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "tok_abc@github.com") {
				t.Errorf("set-url args missing injected token: %q", joined)
			}
		}
		if strings.Contains(cmd, "git rev-parse") {
			return "feed123\n", nil
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder
	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		GitToken:  "tok_abc",
	}
	if err := m.deployGit(req, appRoot, "main", nil, status, &log); err != nil {
		t.Errorf("deployGit() error = %v", err)
	}
	if !sawSetURL {
		t.Error("expected 'git remote set-url' to be called when token provided for existing repo")
	}
	if !strings.Contains(log.String(), "Using access token") {
		t.Error("expected token-auth log line")
	}
}

// detectBuildCmd fallback path inside deployGit (empty BuildCmd, package.json present).
func TestDeployGit_DetectBuildCmd(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)
	os.WriteFile(filepath.Join(appRoot, "package.json"), []byte(`{"scripts":{"build":"x"}}`), 0644)

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}
	var ranBuild string
	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		ranBuild = command
		return "ok", nil
	}

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder
	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "", // triggers detectBuildCmd
	}
	if err := m.deployGit(req, appRoot, "main", nil, status, &log); err != nil {
		t.Errorf("deployGit() error = %v", err)
	}
	if ranBuild != "npm install && npm run build" {
		t.Errorf("detected build cmd = %q, want npm install && npm run build", ranBuild)
	}
}

// ---------------------------------------------------------------------------
// deployDocker: remaining branches
// ---------------------------------------------------------------------------

func TestDeployDocker_DefaultDockerfileAndNetwork(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	var buildArgs []string
	var runArgs []string

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "docker build") {
			buildArgs = append([]string{}, args...)
			return "built", nil
		}
		if strings.Contains(cmd, "docker run") {
			runArgs = append([]string{}, args...)
			return "containerabcdef123456\n", nil
		}
		if strings.Contains(cmd, "docker port") {
			return "0.0.0.0:3000\n", nil
		}
		return "", nil
	}
	waitForAppFn = func(addr string, timeout time.Duration) error { return nil }

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder
	req := DeployRequest{
		Domain:        "test.com",
		DockerFile:    "", // defaults to "Dockerfile"
		DockerPort:    3000,
		DockerNetwork: "mynet",
	}
	if err := m.deployDocker(req, appRoot, status, &log); err != nil {
		t.Errorf("deployDocker() error = %v", err)
	}

	// default dockerfile used in build -f
	joinedBuild := strings.Join(buildArgs, " ")
	if !strings.Contains(joinedBuild, "-f Dockerfile") {
		t.Errorf("build args = %q, want default Dockerfile", joinedBuild)
	}
	// network flag passed to run
	joinedRun := strings.Join(runArgs, " ")
	if !strings.Contains(joinedRun, "--network mynet") {
		t.Errorf("run args = %q, want --network mynet", joinedRun)
	}
	// container ID truncated to 12 chars
	if !strings.Contains(log.String(), "Container: containerabc\n") {
		t.Errorf("log missing truncated container id: %q", log.String())
	}
}

func TestDeployDocker_HealthCheckFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "docker build") {
			return "built", nil
		}
		if strings.Contains(cmd, "docker run") {
			return "short\n", nil // <12 chars, exercises the no-truncate branch
		}
		return "", nil
	}
	waitForAppFn = func(addr string, timeout time.Duration) error {
		return fmt.Errorf("not up")
	}

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder
	req := DeployRequest{Domain: "test.com", DockerFile: "Dockerfile", DockerPort: 3000}

	err := m.deployDocker(req, appRoot, status, &log)
	if err == nil {
		t.Fatal("deployDocker() expected health check error")
	}
	if !strings.Contains(err.Error(), "app health check failed") {
		t.Errorf("error = %v, want 'app health check failed'", err)
	}
}

// docker port command failing should be tolerated (no Listening log, still healthy).
func TestDeployDocker_PortCmdFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "docker build") {
			return "built", nil
		}
		if strings.Contains(cmd, "docker run") {
			return "containerabcdef123456\n", nil
		}
		if strings.Contains(cmd, "docker port") {
			return "no such port", fmt.Errorf("exit status 1")
		}
		return "", nil
	}
	waitForAppFn = func(addr string, timeout time.Duration) error { return nil }

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder
	req := DeployRequest{Domain: "test.com", DockerFile: "Dockerfile", DockerPort: 3000}

	if err := m.deployDocker(req, appRoot, status, &log); err != nil {
		t.Errorf("deployDocker() error = %v", err)
	}
	if strings.Contains(log.String(), "Listening:") {
		t.Error("did not expect Listening log when docker port fails")
	}
}

// ---------------------------------------------------------------------------
// Deploy (public, async) orchestration via mocked hooks
// ---------------------------------------------------------------------------

func TestDeploy_GitSuccess(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "git clone") {
			os.MkdirAll(gitDir, 0755)
			return "cloned", nil
		}
		if strings.Contains(cmd, "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}
	runShellFn = func(dir string, env map[string]string, command string) (string, error) { return "ok", nil }

	m := New(nil)
	done := make(chan error, 1)
	m.Deploy(DeployRequest{
		Domain:    "deploy-ok.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "npm install",
	}, appRoot, func(err error) { done <- err })

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Deploy onComplete error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deploy did not complete in time")
	}

	st := m.Status("deploy-ok.com")
	if st == nil {
		t.Fatal("expected status entry")
	}
	if st.Status != "running" {
		t.Errorf("status = %q, want running", st.Status)
	}
	if st.Mode != "git" {
		t.Errorf("mode = %q, want git", st.Mode)
	}
	if st.Duration == "" {
		t.Error("expected duration to be set")
	}
}

func TestDeploy_GitFailureSetsFailed(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git clone") {
			return "boom", fmt.Errorf("exit 128")
		}
		return "", nil
	}

	m := New(nil)
	done := make(chan error, 1)
	m.Deploy(DeployRequest{
		Domain:    "deploy-fail.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
	}, appRoot, func(err error) { done <- err })

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from failed deploy")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deploy did not complete in time")
	}

	st := m.Status("deploy-fail.com")
	if st == nil || st.Status != "failed" {
		t.Errorf("status = %+v, want failed", st)
	}
	if st != nil && st.Error == "" {
		t.Error("expected error message recorded in status")
	}
}

func TestDeploy_DockerMode(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "docker build") {
			return "built", nil
		}
		if strings.Contains(cmd, "docker run") {
			return "containerabcdef123456\n", nil
		}
		if strings.Contains(cmd, "docker port") {
			return "0.0.0.0:3000\n", nil
		}
		return "", nil
	}
	waitForAppFn = func(addr string, timeout time.Duration) error { return nil }

	m := New(nil)
	done := make(chan error, 1)
	m.Deploy(DeployRequest{
		Domain:     "docker.com",
		DockerFile: "Dockerfile",
		DockerPort: 3000,
	}, appRoot, func(err error) { done <- err })

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Deploy(docker) error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deploy(docker) did not complete in time")
	}

	st := m.Status("docker.com")
	if st == nil || st.Mode != "docker" {
		t.Errorf("status mode = %+v, want docker", st)
	}
}

func TestDeploy_ConcurrentGuard(t *testing.T) {
	defer saveAndRestoreHooks()()

	m := New(nil)
	// Seed an in-progress deploy.
	m.deploys["busy.com"] = &DeployStatus{Domain: "busy.com", Status: "building"}

	called := false
	var gotErr error
	m.Deploy(DeployRequest{
		Domain:    "busy.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
	}, t.TempDir(), func(err error) {
		called = true
		gotErr = err
	})

	if !called {
		t.Fatal("expected onComplete to be called")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "already in progress") {
		t.Errorf("error = %v, want 'already in progress'", gotErr)
	}
}

func TestDeploy_BadURLNilCallback(t *testing.T) {
	// nil onComplete branches must not panic.
	m := New(nil)
	m.Deploy(DeployRequest{Domain: "x.com", GitURL: "ext::evil"}, t.TempDir(), nil)
	m.Deploy(DeployRequest{Domain: "x.com", GitURL: "https://h/r.git", GitBranch: "bad;rm"}, t.TempDir(), nil)

	m2 := New(nil)
	m2.deploys["busy.com"] = &DeployStatus{Domain: "busy.com", Status: "deploying"}
	m2.Deploy(DeployRequest{Domain: "busy.com", GitURL: "https://h/r.git"}, t.TempDir(), nil)
}

// Re-deploy over a previously finished (non in-progress) deploy is allowed.
func TestDeploy_OverFinishedDeploy(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	m := New(nil)
	m.deploys["redeploy.com"] = &DeployStatus{Domain: "redeploy.com", Status: "running"}

	done := make(chan error, 1)
	m.Deploy(DeployRequest{
		Domain:    "redeploy.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "none",
	}, appRoot, func(err error) { done <- err })

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("re-deploy error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("re-deploy did not complete")
	}
}

// Deploy with a real logger exercises the m.logger != nil success + failure log branches.
func TestDeploy_WithLogger(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)

	// First: success path with logger.
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	log := logger.New("error", "text")
	m := New(log)

	done := make(chan error, 1)
	m.Deploy(DeployRequest{
		Domain:    "logged.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "none",
	}, appRoot, func(err error) { done <- err })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Deploy error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deploy did not complete")
	}

	// Second: failure path with logger.
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git fetch") {
			return "boom", fmt.Errorf("exit 128")
		}
		return "", nil
	}
	done2 := make(chan error, 1)
	m.Deploy(DeployRequest{
		Domain:    "logged-fail.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "none",
	}, appRoot, func(err error) { done2 <- err })
	select {
	case err := <-done2:
		if err == nil {
			t.Fatal("expected failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deploy(fail) did not complete")
	}
}

// Token + fetch error exercises redactURL replacing the token in logged output.
func TestDeployGit_RedactsTokenOnError(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "remote set-url") {
			return "", nil
		}
		if strings.Contains(cmd, "git fetch") {
			// Output leaks the URL with the token embedded.
			return "fatal: unable to access https://supersecret@github.com/user/repo.git", fmt.Errorf("exit 128")
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{Domain: "test.com", Status: "deploying", StartedAt: time.Now()}
	var log strings.Builder
	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		GitToken:  "supersecret",
	}
	err := m.deployGit(req, appRoot, "main", nil, status, &log)
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("token leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("expected redacted token marker, got: %v", err)
	}
}

// runShellImpl on the real implementation with a valid command (covers exec path).
func TestRunShellImpl_Direct(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based test")
	}
	out, err := runShellImpl("", nil, "echo direct-impl")
	if err != nil {
		t.Fatalf("runShellImpl error = %v", err)
	}
	if !strings.Contains(out, "direct-impl") {
		t.Errorf("output = %q, want direct-impl", out)
	}
}
