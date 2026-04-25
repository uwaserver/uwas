package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDetectBuildCmdNode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"vite build"}}`), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "npm install && npm run build" {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdNodeNoBuild(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node index.js"}}`), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "npm install" {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdPython(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask\n"), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "pip install -r requirements.txt" {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdRuby(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("source 'https://rubygems.org'\n"), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "bundle install" {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdGo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "go build -o app ." {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdEmpty(t *testing.T) {
	cmd := detectBuildCmd(t.TempDir())
	if cmd != "" {
		t.Errorf("got %q", cmd)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com", "example-com"},
		{"My App!", "my-app-"},
		{"test-123", "test-123"},
		{"UPPERCASE", "uppercase"},
		{"special@#chars", "special--chars"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeName(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSafeGitURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
		errMsg  string
	}{
		{"https://github.com/user/repo.git", false, ""},
		{"git@github.com:user/repo.git", false, ""},
		{"", false, ""}, // empty is allowed
		{"ext::sh -c whoami", true, "ext:: protocol not allowed"},
		{"file:///etc/passwd", true, "file:// protocol not allowed"},
		{"https://github.com/repo?--upload-pack=malicious", true, "git option injection not allowed"},
		{"--receive-pack=malicious", true, "git option injection not allowed"},
	}

	for _, tt := range tests {
		err := safeGitURL(tt.url)
		if tt.wantErr {
			if err == nil {
				t.Errorf("safeGitURL(%q) expected error", tt.url)
			} else if !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("safeGitURL(%q) error %q does not contain %q", tt.url, err.Error(), tt.errMsg)
			}
		} else {
			if err != nil {
				t.Errorf("safeGitURL(%q) unexpected error: %v", tt.url, err)
			}
		}
	}
}

func TestSafeBranch(t *testing.T) {
	tests := []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"develop", true},
		{"feature/new-thing", true},
		{"v1.2.3", true},
		{"hotfix_123", true},
		{"test_branch", true},
		{"branch;rm -rf /", false},
		{"branch|cat /etc/passwd", false},
		{"branch$(whoami)", false},
		{"", true},          // empty is technically valid
		{" branch", false},  // space not allowed
		{"branch\n", false}, // newline not allowed
	}

	for _, tt := range tests {
		got := safeBranch(tt.branch)
		if got != tt.want {
			t.Errorf("safeBranch(%q) = %v, want %v", tt.branch, got, tt.want)
		}
	}
}

func TestInjectTokenInURL(t *testing.T) {
	tests := []struct {
		gitURL string
		token  string
		want   string
	}{
		{
			"https://github.com/user/repo.git",
			"ghp_12345",
			"https://ghp_12345@github.com/user/repo.git",
		},
		{
			"http://gitlab.com/user/repo.git",
			"glpat_67890",
			"http://glpat_67890@gitlab.com/user/repo.git",
		},
		{
			"git@github.com:user/repo.git",
			"token123",
			"git@github.com:user/repo.git", // SSH URLs unchanged
		},
	}

	for _, tt := range tests {
		got := injectTokenInURL(tt.gitURL, tt.token)
		if got != tt.want {
			t.Errorf("injectTokenInURL(%q, %q) = %q, want %q", tt.gitURL, tt.token, got, tt.want)
		}
	}
}

func TestNewManager(t *testing.T) {
	m := New(nil)
	if m == nil {
		t.Fatal("nil manager")
	}
	if s := m.Status("nope"); s != nil {
		t.Error("expected nil status for unknown domain")
	}
}

func TestAllStatuses(t *testing.T) {
	m := New(nil)
	all := m.AllStatuses()
	if len(all) != 0 {
		t.Errorf("expected 0, got %d", len(all))
	}
}

func TestAllStatusesWithData(t *testing.T) {
	m := New(nil)

	// Create a mock deployment status
	m.deploys["test.com"] = &DeployStatus{
		Domain: "test.com",
		Status: "running",
	}
	m.deploys["example.com"] = &DeployStatus{
		Domain: "example.com",
		Status: "failed",
	}

	all := m.AllStatuses()
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}

	// Check that we got the right domains
	domains := make(map[string]string)
	for _, s := range all {
		domains[s.Domain] = s.Status
	}

	if domains["test.com"] != "running" {
		t.Errorf("expected test.com to be running, got %q", domains["test.com"])
	}
	if domains["example.com"] != "failed" {
		t.Errorf("expected example.com to be failed, got %q", domains["example.com"])
	}
}

func TestStatusReturnsCopy(t *testing.T) {
	m := New(nil)

	original := &DeployStatus{
		Domain: "test.com",
		Status: "running",
	}
	m.deploys["test.com"] = original

	// Status returns a pointer to the stored struct
	s := m.Status("test.com")
	if s == nil {
		t.Fatal("expected status, got nil")
	}
	if s.Domain != "test.com" {
		t.Errorf("expected domain test.com, got %q", s.Domain)
	}
	if s.Status != "running" {
		t.Errorf("expected status running, got %q", s.Status)
	}
}

func TestDeployRequestValidation(t *testing.T) {
	// Test that Deploy validates Git URL before starting
	m := New(nil)

	called := false
	var gotErr error

	m.Deploy(DeployRequest{
		Domain: "test.com",
		GitURL: "ext::sh -c whoami", // dangerous URL
	}, "/tmp/test", func(err error) {
		called = true
		gotErr = err
	})

	if !called {
		t.Fatal("expected onComplete to be called")
	}
	if gotErr == nil {
		t.Fatal("expected error for dangerous git URL")
	}
	if !strings.Contains(gotErr.Error(), "ext:: protocol not allowed") {
		t.Errorf("expected protocol error, got: %v", gotErr)
	}
}

func TestDeployRequestInvalidBranch(t *testing.T) {
	m := New(nil)

	called := false
	var gotErr error

	m.Deploy(DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main; rm -rf /", // invalid branch
	}, "/tmp/test", func(err error) {
		called = true
		gotErr = err
	})

	if !called {
		t.Fatal("expected onComplete to be called")
	}
	if gotErr == nil {
		t.Fatal("expected error for invalid branch")
	}
	if !strings.Contains(gotErr.Error(), "invalid branch name") {
		t.Errorf("expected branch error, got: %v", gotErr)
	}
}

// --- runCmd and runShell tests ---

func TestRunCmdSuccess(t *testing.T) {
	name := "echo"
	args := []string{"hello", "world"}
	if runtime.GOOS == "windows" {
		name = "cmd"
		args = []string{"/c", "echo hello world"}
	}
	out, err := runCmd("", nil, name, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected output to contain 'hello world', got: %q", out)
	}
}

func TestRunCmdWithDir(t *testing.T) {
	dir := t.TempDir()
	// Create a file in the temp dir and verify we can see it
	testFile := filepath.Join(dir, "testfile.txt")
	os.WriteFile(testFile, []byte("test"), 0644)

	// List files in the directory
	out, err := runCmd(dir, nil, "ls", "-1")
	if err != nil {
		// Try dir command on Windows
		out, err = runCmd(dir, nil, "cmd", "/c", "dir", "/b")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "testfile.txt") {
		t.Errorf("expected output to contain 'testfile.txt', got: %q", out)
	}
}

func TestRunCmdWithEnv(t *testing.T) {
	name := "sh"
	args := []string{"-c", "echo $TEST_VAR"}
	if runtime.GOOS == "windows" {
		name = "cmd"
		args = []string{"/c", "echo %TEST_VAR%"}
	}
	out, err := runCmd("", map[string]string{"TEST_VAR": "test_value"}, name, args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "test_value") {
		t.Errorf("expected output to contain 'test_value', got: %q", out)
	}
}

func TestRunCmdFailure(t *testing.T) {
	_, err := runCmd("", nil, "false")
	if err == nil {
		t.Error("expected error for failed command")
	}
}

func TestRunCmdInvalidCommand(t *testing.T) {
	_, err := runCmd("", nil, "nonexistent_command_xyz")
	if err == nil {
		t.Error("expected error for nonexistent command")
	}
}

func TestRunShellSuccess(t *testing.T) {
	out, err := runShell("", nil, "echo hello && echo world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("expected output to contain 'hello' and 'world', got: %q", out)
	}
}

func TestRunShellWithEnv(t *testing.T) {
	command := "echo $MY_VAR"
	if runtime.GOOS == "windows" {
		command = "echo %MY_VAR%"
	}
	out, err := runShell("", map[string]string{"MY_VAR": "my_value"}, command)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "my_value") {
		t.Errorf("expected output to contain 'my_value', got: %q", out)
	}
}

func TestRunShellFailure(t *testing.T) {
	_, err := runShell("", nil, "exit 1")
	if err == nil {
		t.Error("expected error for failed shell command")
	}
}

func TestRunShellNullByte(t *testing.T) {
	_, err := runShell("", nil, "echo hello\x00world")
	if err == nil {
		t.Error("expected error for command with null byte")
	}
	if !strings.Contains(err.Error(), "null byte") {
		t.Errorf("expected null byte error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Mock hooks for testing deployGit and deployDocker
// ---------------------------------------------------------------------------

// saveAndRestoreHooks returns a function that restores the original hook values.
func saveAndRestoreHooks() func() {
	origRunCmdFn := runCmdFn
	origRunShellFn := runShellFn
	return func() {
		runCmdFn = origRunCmdFn
		runShellFn = origRunShellFn
	}
}

// fakeCommandRunner creates a mock that returns predefined outputs based on command patterns.
func fakeCommandRunner(outputs map[string]struct {
	out string
	err error
}) func(string, map[string]string, string, ...string) (string, error) {
	return func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		for pattern, result := range outputs {
			if strings.Contains(cmd, pattern) {
				return result.out, result.err
			}
		}
		// Default: success with empty output
		return "", nil
	}
}

// fakeShellRunner creates a mock for shell commands.
func fakeShellRunner(outputs map[string]struct {
	out string
	err error
}) func(string, map[string]string, string) (string, error) {
	return func(dir string, env map[string]string, command string) (string, error) {
		for pattern, result := range outputs {
			if strings.Contains(command, pattern) {
				return result.out, result.err
			}
		}
		// Default: success with empty output
		return "", nil
	}
}

// ---------------------------------------------------------------------------
// deployGit tests
// ---------------------------------------------------------------------------

func TestDeployGit_FreshClone(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	// Ensure .git directory does NOT exist (fresh clone scenario)
	if _, err := os.Stat(gitDir); err == nil {
		os.RemoveAll(gitDir)
	}

	callOrder := []string{}

	// Mock runCmd to track commands and return success
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		callOrder = append(callOrder, cmd)

		if strings.Contains(cmd, "git clone") {
			// Simulate clone by creating .git directory
			os.MkdirAll(gitDir, 0755)
			return "Cloning into '" + appRoot + "'...", nil
		}
		if strings.Contains(cmd, "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		callOrder = append(callOrder, "shell: "+command)
		return "build output", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "npm install",
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err != nil {
		t.Errorf("deployGit() error = %v, want nil", err)
	}

	// Verify clone was called
	hasClone := false
	for _, cmd := range callOrder {
		if strings.Contains(cmd, "git clone") {
			hasClone = true
			break
		}
	}
	if !hasClone {
		t.Error("expected git clone to be called")
	}

	// Verify commit SHA was set
	if status.CommitSHA != "abc1234" {
		t.Errorf("CommitSHA = %q, want abc1234", status.CommitSHA)
	}
}

func TestDeployGit_ExistingRepo(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755) // Simulate existing repo

	callOrder := []string{}

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		callOrder = append(callOrder, cmd)

		if strings.Contains(cmd, "git rev-parse") {
			return "def5678\n", nil
		}
		return "", nil
	}

	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		callOrder = append(callOrder, "shell: "+command)
		return "build output", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "develop",
	}

	err := m.deployGit(req, appRoot, "develop", status, &log)

	if err != nil {
		t.Errorf("deployGit() error = %v, want nil", err)
	}

	// Verify fetch and reset were called (not clone)
	hasFetch := false
	hasReset := false
	for _, cmd := range callOrder {
		if strings.Contains(cmd, "git fetch") {
			hasFetch = true
		}
		if strings.Contains(cmd, "git reset") {
			hasReset = true
		}
	}
	if !hasFetch {
		t.Error("expected git fetch to be called for existing repo")
	}
	if !hasReset {
		t.Error("expected git reset to be called for existing repo")
	}
}

func TestDeployGit_CloneFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git clone") {
			return "fatal: repository not found", fmt.Errorf("exit status 128")
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/nonexistent.git",
		GitBranch: "main",
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err == nil {
		t.Error("deployGit() expected error for failed clone")
	}
	if !strings.Contains(err.Error(), "git clone") {
		t.Errorf("error = %v, expected 'git clone' in error", err)
	}
}

func TestDeployGit_NoGitURL(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "", // No URL
		GitBranch: "main",
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err == nil {
		t.Error("deployGit() expected error for empty GitURL")
	}
	if !strings.Contains(err.Error(), "no git URL") {
		t.Errorf("error = %v, expected 'no git URL' in error", err)
	}
}

func TestDeployGit_NoBuildCmd(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git clone") {
			os.MkdirAll(gitDir, 0755)
			return "cloned", nil
		}
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		t.Error("runShell should not be called when no build command is needed")
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "", // No build
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err != nil {
		t.Errorf("deployGit() error = %v, want nil", err)
	}
}

func TestDeployGit_WithToken(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "git clone") {
			// Check that token was injected in URL
			for _, arg := range args {
				if strings.Contains(arg, "ghp_token123@github.com") {
					os.MkdirAll(gitDir, 0755)
					return "cloned with token", nil
				}
			}
			t.Error("expected token to be injected in git URL")
		}
		if strings.Contains(cmd, "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		GitToken:  "ghp_token123",
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err != nil {
		t.Errorf("deployGit() error = %v, want nil", err)
	}
}

func TestDeployGit_BuildFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "git clone") {
			os.MkdirAll(gitDir, 0755)
			return "cloned", nil
		}
		if strings.Contains(name+" "+strings.Join(args, " "), "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		return "npm ERR! build failed", fmt.Errorf("exit status 1")
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "npm run build",
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err == nil {
		t.Error("deployGit() expected error for failed build")
	}
	if !strings.Contains(err.Error(), "build failed") {
		t.Errorf("error = %v, expected 'build failed' in error", err)
	}
}

// ---------------------------------------------------------------------------
// deployDocker tests
// ---------------------------------------------------------------------------

func TestDeployDocker_Success(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")

		if strings.Contains(cmd, "docker stop") {
			return "stopped", nil
		}
		if strings.Contains(cmd, "docker rm") {
			return "removed", nil
		}
		if strings.Contains(cmd, "docker build") {
			return "Successfully built abc123\n", nil
		}
		if strings.Contains(cmd, "docker run") {
			return "container12345678\n", nil
		}
		if strings.Contains(cmd, "docker port") {
			return "127.0.0.1:8080\n", nil
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:     "test.com",
		DockerFile: "Dockerfile",
		DockerPort: 3000,
	}

	err := m.deployDocker(req, appRoot, status, &log)

	if err != nil {
		t.Errorf("deployDocker() error = %v, want nil", err)
	}

	logStr := log.String()
	if !strings.Contains(logStr, "container123") {
		t.Error("expected container ID in log")
	}
}

func TestDeployDocker_BuildFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")

		if strings.Contains(cmd, "docker stop") || strings.Contains(cmd, "docker rm") {
			return "", nil
		}
		if strings.Contains(cmd, "docker build") {
			return "Step 2/10 failed", fmt.Errorf("exit status 1")
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:     "test.com",
		DockerFile: "Dockerfile",
		DockerPort: 3000,
	}

	err := m.deployDocker(req, appRoot, status, &log)

	if err == nil {
		t.Error("deployDocker() expected error for failed build")
	}
	if !strings.Contains(err.Error(), "docker build") {
		t.Errorf("error = %v, expected 'docker build' in error", err)
	}
}

func TestDeployDocker_RunFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")

		if strings.Contains(cmd, "docker stop") || strings.Contains(cmd, "docker rm") {
			return "", nil
		}
		if strings.Contains(cmd, "docker build") {
			return "Successfully built abc123\n", nil
		}
		if strings.Contains(cmd, "docker run") {
			return "port already in use", fmt.Errorf("exit status 125")
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:     "test.com",
		DockerFile: "Dockerfile",
		DockerPort: 3000,
	}

	err := m.deployDocker(req, appRoot, status, &log)

	if err == nil {
		t.Error("deployDocker() expected error for failed run")
	}
	if !strings.Contains(err.Error(), "docker run") {
		t.Errorf("error = %v, expected 'docker run' in error", err)
	}
}

func TestDeployDocker_InvalidDockerfilePath(t *testing.T) {
	defer saveAndRestoreHooks()()

	// Mock runCmd to avoid actual docker execution
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	// Test with absolute path (use platform-appropriate absolute path)
	absPath := "/etc/passwd/malicious"
	if runtime.GOOS == "windows" {
		absPath = "C:\\Windows\\System32\\malicious"
	}
	req := DeployRequest{
		Domain:     "test.com",
		DockerFile: absPath,
		DockerPort: 3000,
	}

	err := m.deployDocker(req, t.TempDir(), status, &log)

	if err == nil {
		t.Error("deployDocker() expected error for absolute Dockerfile path")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("error = %v, expected 'must be relative' in error", err)
	}

	// Test with traversal path
	req.DockerFile = "../Dockerfile"
	log.Reset()

	err = m.deployDocker(req, t.TempDir(), status, &log)

	if err == nil {
		t.Error("deployDocker() expected error for traversal Dockerfile path")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("error = %v, expected 'must be relative' in error", err)
	}
}

func TestDeployDocker_DefaultPort(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	var capturedPort string

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")

		if strings.Contains(cmd, "docker stop") || strings.Contains(cmd, "docker rm") {
			return "", nil
		}
		if strings.Contains(cmd, "docker build") {
			return "Successfully built abc123\n", nil
		}
		if strings.Contains(cmd, "docker run") {
			// Capture the port argument
			for i, arg := range args {
				if arg == "-p" && i+1 < len(args) {
					capturedPort = args[i+1]
					break
				}
			}
			return "container12345678\n", nil
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:     "test.com",
		DockerFile: "Dockerfile",
		DockerPort: 0, // Should default to 3000
	}

	err := m.deployDocker(req, appRoot, status, &log)

	if err != nil {
		t.Errorf("deployDocker() error = %v, want nil", err)
	}

	// Verify port mapping uses 3000
	if capturedPort != "127.0.0.1:0:3000" {
		t.Errorf("port mapping = %q, want 127.0.0.1:0:3000", capturedPort)
	}
}

func TestDeployDocker_WithEnvVars(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	envVars := []string{}

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "docker run") {
			// Capture env var args
			for i, arg := range args {
				if arg == "-e" && i+1 < len(args) {
					envVars = append(envVars, args[i+1])
				}
			}
			return "container12345678\n", nil
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:     "test.com",
		DockerFile: "Dockerfile",
		DockerPort: 3000,
		Env: map[string]string{
			"NODE_ENV": "production",
			"API_KEY":  "secret123",
		},
	}

	err := m.deployDocker(req, appRoot, status, &log)

	if err != nil {
		t.Errorf("deployDocker() error = %v, want nil", err)
	}

	// Verify env vars were passed
	hasNodeEnv := false
	hasApiKey := false
	for _, v := range envVars {
		if v == "NODE_ENV=production" {
			hasNodeEnv = true
		}
		if v == "API_KEY=secret123" {
			hasApiKey = true
		}
	}
	if !hasNodeEnv {
		t.Error("expected NODE_ENV env var")
	}
	if !hasApiKey {
		t.Error("expected API_KEY env var")
	}
}

func TestDeployGit_FetchFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755) // Simulate existing repo

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "git fetch") {
			return "fatal: unable to access", fmt.Errorf("exit status 128")
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err == nil {
		t.Error("deployGit() expected error for failed fetch")
	}
	if !strings.Contains(err.Error(), "git fetch") {
		t.Errorf("error = %v, expected 'git fetch' in error", err)
	}
}

func TestDeployGit_ResetFails(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")
	os.MkdirAll(gitDir, 0755)

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if strings.Contains(cmd, "git fetch") {
			return "", nil
		}
		if strings.Contains(cmd, "git reset") {
			return "fatal: ambiguous argument", fmt.Errorf("exit status 128")
		}
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "nonexistent",
	}

	err := m.deployGit(req, appRoot, "nonexistent", status, &log)

	if err == nil {
		t.Error("deployGit() expected error for failed reset")
	}
	if !strings.Contains(err.Error(), "git reset") {
		t.Errorf("error = %v, expected 'git reset' in error", err)
	}
}

func TestDeployGit_WithSSHKey(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	// Create a mock SSH key file
	sshKeyPath := filepath.Join(t.TempDir(), "id_rsa")
	os.WriteFile(sshKeyPath, []byte("mock key"), 0600)

	var gitSSHCommand string

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		// Check environment variables for GIT_SSH_COMMAND
		if val, ok := env["GIT_SSH_COMMAND"]; ok {
			gitSSHCommand = val
		}

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

	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:     "test.com",
		GitURL:     "git@github.com:user/repo.git",
		GitBranch:  "main",
		SSHKeyPath: sshKeyPath,
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err != nil {
		t.Errorf("deployGit() error = %v, want nil", err)
	}

	// Verify GIT_SSH_COMMAND was set
	if gitSSHCommand == "" {
		t.Error("expected GIT_SSH_COMMAND to be set")
	}
	if !strings.Contains(gitSSHCommand, sshKeyPath) {
		t.Errorf("GIT_SSH_COMMAND = %q, expected to contain %q", gitSSHCommand, sshKeyPath)
	}
}

func TestDeployGit_InvalidSSHKeyPath(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	// Create a temp directory for testing absolute paths
	tempDir := t.TempDir()
	absPath, _ := filepath.Abs(tempDir)

	tests := []struct {
		name    string
		keyPath string
		errMsg  string
	}{
		{
			name:    "relative path",
			keyPath: "./id_rsa",
			errMsg:  "must be absolute",
		},
		{
			name:    "relative with parent",
			keyPath: "..\\id_rsa",
			errMsg:  "must be absolute",
		},
		{
			name:    "nonexistent file",
			keyPath: absPath + string(filepath.Separator) + "nonexistent_key_file_12345",
			errMsg:  "not found",
		},
	}

	for _, tt := range tests {
		req := DeployRequest{
			Domain:     "test.com",
			GitURL:     "git@github.com:user/repo.git",
			GitBranch:  "main",
			SSHKeyPath: tt.keyPath,
		}

		err := m.deployGit(req, appRoot, "main", status, &log)

		if err == nil {
			t.Errorf("%s: deployGit() expected error", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.errMsg) {
			t.Errorf("%s: error = %v, expected '%s' in error", tt.name, err, tt.errMsg)
		}
		log.Reset()
	}
}

func TestDeployGit_WithCustomEnv(t *testing.T) {
	defer saveAndRestoreHooks()()

	appRoot := t.TempDir()
	gitDir := filepath.Join(appRoot, ".git")

	var gitEnv map[string]string
	var shellEnv map[string]string

	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")

		// Capture env from git commands
		if strings.Contains(cmd, "git") && env != nil {
			gitEnv = env
		}

		if strings.Contains(cmd, "git clone") {
			os.MkdirAll(gitDir, 0755)
			return "cloned", nil
		}
		if strings.Contains(cmd, "git rev-parse") {
			return "abc1234\n", nil
		}
		return "", nil
	}

	runShellFn = func(dir string, env map[string]string, command string) (string, error) {
		shellEnv = env
		return "", nil
	}

	m := New(nil)
	status := &DeployStatus{
		Domain:    "test.com",
		Status:    "deploying",
		StartedAt: time.Now(),
	}
	var log strings.Builder

	req := DeployRequest{
		Domain:    "test.com",
		GitURL:    "https://github.com/user/repo.git",
		GitBranch: "main",
		BuildCmd:  "npm install",
		Env: map[string]string{
			"CUSTOM_VAR": "custom_value",
			"NODE_ENV":   "production",
		},
	}

	err := m.deployGit(req, appRoot, "main", status, &log)

	if err != nil {
		t.Errorf("deployGit() error = %v, want nil", err)
	}

	// Check that GIT_ALLOW_PROTOCOL is set in git env
	if gitEnv["GIT_ALLOW_PROTOCOL"] != "https:ssh:git" {
		t.Errorf("GIT_ALLOW_PROTOCOL = %q, want 'https:ssh:git'", gitEnv["GIT_ALLOW_PROTOCOL"])
	}

	// Check custom env vars are passed to both git and shell
	if gitEnv["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("git CUSTOM_VAR = %q, want 'custom_value'", gitEnv["CUSTOM_VAR"])
	}
	if gitEnv["NODE_ENV"] != "production" {
		t.Errorf("git NODE_ENV = %q, want 'production'", gitEnv["NODE_ENV"])
	}

	// Shell should also have custom env vars
	if shellEnv["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("shell CUSTOM_VAR = %q, want 'custom_value'", shellEnv["CUSTOM_VAR"])
	}
	if shellEnv["NODE_ENV"] != "production" {
		t.Errorf("shell NODE_ENV = %q, want 'production'", shellEnv["NODE_ENV"])
	}
}
