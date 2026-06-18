package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
)

func TestValidateGitURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://github.com/user/repo.git", true},
		{"ssh://git@github.com/user/repo.git", true},
		{"git@github.com:user/repo.git", true},
		// Rejected schemes / injection
		{"", false},
		{"ext::sh -c whoami", false},
		{"file:///etc/passwd", false},
		{"http://github.com/user/repo.git", false}, // http (not https) rejected
		{"https://github.com/user/repo.git --upload-pack=whoami", false},
		{"https://example.com/repo.git\nmalicious", false},
		{"git://github.com/user/repo.git", false}, // git:// is plaintext, reject
	}
	for _, c := range cases {
		err := validateGitURL(c.url)
		if c.ok && err != nil {
			t.Errorf("validateGitURL(%q) unexpected err: %v", c.url, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateGitURL(%q) should have errored", c.url)
		}
	}
}

func TestValidGitRef(t *testing.T) {
	good := []string{"main", "v1.0.0", "feature/x", "release_2", "a-b-c", "1.2.3-rc1"}
	for _, s := range good {
		if !validGitRef(s) {
			t.Errorf("validGitRef(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",
		"main; rm -rf /",
		"main && evil",
		"main`whoami`",
		"main$(whoami)",
		"main|cat /etc/passwd",
		"main\nnewline",
		strings.Repeat("a", 300),
	}
	for _, s := range bad {
		if validGitRef(s) {
			t.Errorf("validGitRef(%q) = true, want false", s)
		}
	}
}

func TestValidateBuildCommand(t *testing.T) {
	good := []string{
		"npm ci",
		"npm ci && npm run build",
		"pip install -r requirements.txt",
		"go build -o ./main",
		"make",
	}
	for _, s := range good {
		if err := validateBuildCommand(s); err != nil {
			t.Errorf("validateBuildCommand(%q) unexpected err: %v", s, err)
		}
	}
	bad := []string{
		"npm ci; rm -rf /",
		"npm ci & rm -rf /",
		"npm ci | tee build.log",
		"npm ci > build.log",
		"echo `whoami`",
		"echo $(whoami)",
		"npm ci\nmalicious",
		"npm ci\x00",
	}
	for _, s := range bad {
		if err := validateBuildCommand(s); err == nil {
			t.Errorf("validateBuildCommand(%q) should have errored", s)
		}
	}
}

func TestDetectAppBuildCmdNodePackageManagers(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		expected string
	}{
		{
			name:     "npm build with package lock",
			files:    map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`, "package-lock.json": "{}"},
			expected: "npm ci && npm run build",
		},
		{
			name:     "pnpm build",
			files:    map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`, "pnpm-lock.yaml": "lockfileVersion: 9"},
			expected: "corepack pnpm install --frozen-lockfile && corepack pnpm run build",
		},
		{
			name:     "yarn install only",
			files:    map[string]string{"package.json": `{"scripts":{"start":"node index.js"}}`, "yarn.lock": ""},
			expected: "corepack yarn install --frozen-lockfile",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if got := detectAppBuildCmd(dir); got != tc.expected {
				t.Fatalf("detectAppBuildCmd = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestDetectAppBuildCmdOtherRuntimes(t *testing.T) {
	cases := []struct {
		file     string
		content  string
		expected string
	}{
		{"requirements.txt", "flask\n", "pip install -r requirements.txt"},
		{"Gemfile", "source 'https://rubygems.org'\n", "bundle install"},
		{"go.mod", "module example.com/app\n", "go build -o main ."},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.content), 0644); err != nil {
			t.Fatal(err)
		}
		if got := detectAppBuildCmd(dir); got != tc.expected {
			t.Fatalf("%s: detectAppBuildCmd = %q, want %q", tc.file, got, tc.expected)
		}
	}
}

func TestGitAuthEnvTokenAndRedaction(t *testing.T) {
	env, cloneURL, cleanup, err := gitAuthEnv("https://github.com/acme/private.git", "", "ghp_secret")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if strings.Contains(cloneURL, "ghp_secret") {
		t.Fatalf("token leaked into clone URL: %q", cloneURL)
	}
	var askpass string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_ASKPASS=") {
			askpass = strings.TrimPrefix(kv, "GIT_ASKPASS=")
		}
	}
	if askpass == "" {
		t.Fatalf("GIT_ASKPASS not configured in env: %#v", env)
	}
	out, err := os.ReadFile(askpass)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "ghp_secret") {
		t.Fatalf("askpass helper does not contain token")
	}
	args := []string{"clone", cloneURL, "/tmp/app"}
	redacted := redactCommandArgs(args)
	if strings.Contains(redacted, "ghp_secret") {
		t.Fatalf("token leaked in redacted command: %q", redacted)
	}
	if strings.Contains(redacted, "***") {
		t.Fatalf("clean https URL should not need masking: %q", redacted)
	}
	cleanup()
	if _, err := os.Stat(askpass); !os.IsNotExist(err) {
		t.Fatalf("askpass helper was not removed, stat err=%v", err)
	}
}

func TestGitAuthEnvSSHKeyConvertsKnownHTTPSURL(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0600); err != nil {
		t.Fatal(err)
	}

	env, cloneURL, cleanup, err := gitAuthEnv("https://github.com/acme/private.git", keyPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if cloneURL != "git@github.com:acme/private.git" {
		t.Fatalf("clone URL = %q, want SSH URL", cloneURL)
	}
	foundSSHCommand := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") && strings.Contains(kv, keyPath) {
			foundSSHCommand = true
			for _, want := range []string{"-o IdentitiesOnly=yes", "-o BatchMode=yes", "-o ConnectTimeout=15", "-o StrictHostKeyChecking=accept-new"} {
				if !strings.Contains(kv, want) {
					t.Fatalf("GIT_SSH_COMMAND missing %q: %s", want, kv)
				}
			}
		}
	}
	if !foundSSHCommand {
		t.Fatalf("GIT_SSH_COMMAND with key not configured: %#v", env)
	}
}

func TestRunDeployCoreExistingRepoDefaultBranchResetsOriginHead(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	gitLog := filepath.Join(t.TempDir(), "git.log")
	fakeGit := filepath.Join(fakeBin, "git")
	script := `#!/bin/sh
set -eu
echo "$@" >> "$FAKE_GIT_LOG"
if [ "$1" = "symbolic-ref" ]; then
  echo origin/main
fi
`
	if err := os.WriteFile(fakeGit, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GIT_LOG", gitLog)

	var log strings.Builder
	err := runDeployCore(context.Background(), &apps.App{
		Name:    "default-branch",
		Runtime: apps.RuntimeNode,
		WorkDir: dir,
		Deploy:  apps.DeployConfig{GitURL: "https://github.com/acme/app.git"},
	}, "", "", "none", "", "", nil, &log)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "reset --hard origin/main") {
		t.Fatalf("expected reset to remote default branch, got log:\n%s", string(data))
	}
	if strings.Contains(string(data), "reset --hard HEAD") {
		t.Fatalf("must not reset to local HEAD for default branch deploy, log:\n%s", string(data))
	}
}

func TestRunDeployCoreExistingRepoUpdatesOriginURL(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	gitLog := filepath.Join(t.TempDir(), "git.log")
	fakeGit := filepath.Join(fakeBin, "git")
	script := `#!/bin/sh
set -eu
echo "$@" >> "$FAKE_GIT_LOG"
if [ "$1" = "remote" ] && [ "$2" = "get-url" ]; then
  echo https://github.com/acme/old.git
fi
if [ "$1" = "symbolic-ref" ]; then
  echo origin/main
fi
`
	if err := os.WriteFile(fakeGit, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GIT_LOG", gitLog)

	var log strings.Builder
	err := runDeployCore(context.Background(), &apps.App{
		Name:    "changed-origin",
		Runtime: apps.RuntimeNode,
		WorkDir: dir,
		Deploy:  apps.DeployConfig{GitURL: "https://github.com/acme/new.git"},
	}, "", "", "none", "", "", nil, &log)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "remote set-url origin https://github.com/acme/new.git") {
		t.Fatalf("expected origin URL to be updated, got log:\n%s", string(data))
	}
}

func TestAppDeployPreflightChecksRuntimeAndDeployConfig(t *testing.T) {
	origLookPath := execLookPath
	execLookPath = func(file string) (string, error) {
		switch file {
		case "git", "node":
			return "/usr/bin/" + file, nil
		default:
			return "", fmt.Errorf("missing")
		}
	}
	defer func() { execLookPath = origLookPath }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"vite build"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	checks := appDeployPreflight(&apps.App{
		Name:    "preflight",
		Runtime: apps.RuntimeNode,
		WorkDir: dir,
		Deploy:  apps.DeployConfig{GitURL: "https://github.com/acme/app.git"},
	})
	foundGit := false
	foundNode := false
	foundNPMMissing := false
	for _, check := range checks {
		switch check.Name {
		case "git":
			foundGit = check.OK
		case "node":
			foundNode = check.OK
		case "npm":
			foundNPMMissing = !check.OK && check.Required
		}
	}
	if !foundGit || !foundNode || !foundNPMMissing {
		t.Fatalf("unexpected preflight checks: %#v", checks)
	}
}

func TestRecordAppDeployHistoryBoundsLatestFirst(t *testing.T) {
	name := "history-test"
	deployHistoryMu.Lock()
	delete(deployHistory, name)
	deployHistoryMu.Unlock()
	for i := 0; i < 25; i++ {
		recordAppDeployHistory(name, appDeployHistoryEntry{
			Source:    "manual",
			StartedAt: timeNowForTest(i),
			Finished:  timeNowForTest(i),
			OK:        i%2 == 0,
			CommitSHA: fmt.Sprintf("sha-%02d", i),
		})
	}
	deployHistoryMu.Lock()
	items := append([]appDeployHistoryEntry(nil), deployHistory[name]...)
	deployHistoryMu.Unlock()
	if len(items) != 20 {
		t.Fatalf("history len = %d, want 20", len(items))
	}
	if items[0].CommitSHA != "sha-24" || items[len(items)-1].CommitSHA != "sha-05" {
		t.Fatalf("history order/bounds wrong: first=%s last=%s", items[0].CommitSHA, items[len(items)-1].CommitSHA)
	}
}

func TestAppDeployHistoryPersistsAndLoads(t *testing.T) {
	root := t.TempDir()
	items := make([]appDeployHistoryEntry, 0, 25)
	for i := 0; i < 25; i++ {
		items = append(items, appDeployHistoryEntry{
			Source:    "manual",
			StartedAt: timeNowForTest(i),
			Finished:  timeNowForTest(i),
			OK:        i%2 == 0,
			CommitSHA: fmt.Sprintf("sha-%02d", i),
		})
	}
	if err := persistAppDeployHistory(root, "persist-app", items); err != nil {
		t.Fatal(err)
	}
	loaded := loadAppDeployHistory(root, "persist-app")
	if len(loaded) != 20 {
		t.Fatalf("loaded len = %d, want 20", len(loaded))
	}
	if loaded[0].CommitSHA != "sha-00" || loaded[len(loaded)-1].CommitSHA != "sha-19" {
		t.Fatalf("loaded history order/bounds wrong: first=%s last=%s", loaded[0].CommitSHA, loaded[len(loaded)-1].CommitSHA)
	}
	if path := deployHistoryPath(root, "../escape"); path != "" {
		t.Fatalf("unsafe history path = %q, want empty", path)
	}
}

func TestProbeAppHealthRequiresHTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "not ready", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}
	def := &apps.App{Name: "health", Port: port}
	if err := probeAppHealth(def, "/health"); err != nil {
		t.Fatalf("probeAppHealth(/health) unexpected err: %v", err)
	}
	if err := probeAppHealth(def, "/ready"); err == nil {
		t.Fatalf("probeAppHealth(/ready) should fail on 500")
	}
}

func TestValidateHealthPath(t *testing.T) {
	good := []string{"", "/health", "/readyz?deep=1"}
	for _, path := range good {
		if err := validateHealthPath(path); err != nil {
			t.Fatalf("validateHealthPath(%q) unexpected err: %v", path, err)
		}
	}
	bad := []string{"health", "//example.com/health", "/bad\npath", strings.Repeat("a", 513)}
	for _, path := range bad {
		if err := validateHealthPath(path); err == nil {
			t.Fatalf("validateHealthPath(%q) should fail", path)
		}
	}
}

func timeNowForTest(offset int) time.Time {
	return time.Unix(int64(offset), 0).UTC()
}
