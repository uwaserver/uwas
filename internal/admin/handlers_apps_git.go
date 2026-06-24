package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uwaserver/uwas/internal/apps"
)

func runStep(ctx context.Context, wd, name string, args []string, out *strings.Builder, env []string) error {
	out.WriteString(fmt.Sprintf("\n$ %s %s\n", name, redactCommandArgs(args)))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = wd
	if env != nil {
		cmd.Env = env
	}
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	out.Write(combined.Bytes())
	return err
}

// runOutput is runStep without the tee-to-log behavior — used for
// capturing single-line values like the deployed commit SHA.
func runOutput(ctx context.Context, wd, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = wd
	out, err := cmd.Output()
	return string(out), err
}

// runShell runs a build command through /bin/sh -c (or cmd /C on
// Windows). Build commands are usually pipelines like
// `npm ci && npm run build` so a real shell is needed; we lock down
// the surface via validateBuildCommand before getting here.
func runShell(ctx context.Context, wd, command string, out *strings.Builder, env []string) error {
	cmd := buildShellCmd(ctx, command)
	cmd.Dir = wd
	if env != nil {
		cmd.Env = env
	}
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	out.Write(combined.Bytes())
	return err
}

// validateGitURL accepts https://, ssh://, and git@ URIs and rejects
// transport-level injection vectors (ext::, file://, --upload-pack).
// Duplicated from internal/deploy/safeGitURL to avoid an admin →
// deploy package dependency just for one helper.
func validateGitURL(u string) error {
	u = strings.TrimSpace(u)
	if u == "" {
		return fmt.Errorf("git_url is required")
	}
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "ext::") {
		return fmt.Errorf("ext:: protocol not allowed")
	}
	if strings.HasPrefix(lower, "file://") {
		return fmt.Errorf("file:// protocol not allowed")
	}
	if strings.Contains(lower, "--upload-pack") || strings.Contains(lower, "--receive-pack") {
		return fmt.Errorf("git option injection not allowed in URL")
	}
	if !(strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "ssh://") ||
		strings.HasPrefix(lower, "git@")) {
		return fmt.Errorf("only https://, ssh://, and git@ URIs are allowed")
	}
	if strings.ContainsAny(u, " \t\n\r\x00") {
		return fmt.Errorf("git_url contains whitespace or null bytes")
	}
	return nil
}

// validGitRef accepts ref names that match git's own
// `git check-ref-format` rules just enough to keep shell-meaningful
// characters out. Strict enough that `main; rm -rf /` is rejected.
func validGitRef(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.' || r == '/':
		default:
			return false
		}
	}
	return true
}

// validateBuildCommand is the build-step equivalent of the supervisor's
// validateShellCommand. We allow `&&` (chaining build steps is the
// norm: `npm ci && npm run build`) but still reject the most dangerous
// metacharacters and control bytes.
func validateBuildCommand(s string) error {
	if strings.ContainsAny(s, "\x00\n\r") {
		return fmt.Errorf("control characters not allowed")
	}
	for _, f := range []string{"$(", "`", ";"} {
		if strings.Contains(s, f) {
			return fmt.Errorf("forbidden metacharacter: %q", f)
		}
	}
	if strings.ContainsAny(s, "|<>") {
		return fmt.Errorf("forbidden metacharacter: pipe/redirection")
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '&' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '&' {
			i++
			continue
		}
		return fmt.Errorf("forbidden metacharacter: %q", "&")
	}
	return nil
}

// buildShellCmd is split out so a future test can intercept the shell
// invocation. exec.CommandContext is used so the 5-minute deploy
// timeout SIGKILLs a runaway build.
func buildShellCmd(ctx context.Context, command string) *exec.Cmd {
	// /bin/sh on Unix is universal; cmd /C on Windows mirrors what the
	// supervisor uses for the runtime command.
	if isWindows() {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}

// runDeployCore is the shared clone-or-pull + build sequence used by
// BOTH the manual /deploy endpoint and the webhook auto-deploy
// background worker. Operates purely on the workdir; doesn't touch
// the supervisor (caller restarts after this returns).
//
// On success, returns nil and the workdir contains the up-to-date
// source. On failure, the returned error includes which step failed
// and the logBuf contains the full git/build output.
//
// Input validation (URL scheme, branch ref, build command) is the
// CALLER's responsibility — both the manual handler and the webhook
// receiver validate before calling here.
func runDeployCore(
	ctx context.Context,
	def *apps.App,
	gitURL, gitBranch, buildCmd, sshKeyPath, gitToken string,
	extraEnv map[string]string,
	logBuf *strings.Builder,
) error {
	if def == nil || def.WorkDir == "" {
		return fmt.Errorf("app has no work_dir resolved")
	}
	if err := os.MkdirAll(filepath.Dir(def.WorkDir), 0755); err != nil {
		return fmt.Errorf("create workdir parent: %w", err)
	}
	gitURL = strings.TrimSpace(gitURL)
	if gitURL == "" {
		gitURL = strings.TrimSpace(def.Deploy.GitURL)
	}
	gitBranch = strings.TrimSpace(gitBranch)
	if gitBranch == "" {
		gitBranch = strings.TrimSpace(def.Deploy.GitBranch)
	}
	buildCmd = strings.TrimSpace(buildCmd)
	if buildCmd == "" {
		buildCmd = strings.TrimSpace(def.Deploy.BuildCmd)
	}
	sshKeyPath = strings.TrimSpace(sshKeyPath)
	if sshKeyPath == "" {
		sshKeyPath = strings.TrimSpace(def.Deploy.SSHKeyPath)
	}
	gitToken = strings.TrimSpace(gitToken)
	if gitToken == "" {
		gitToken = strings.TrimSpace(def.Deploy.GitToken)
	}
	if err := validateGitURL(gitURL); err != nil {
		return err
	}
	if gitBranch != "" && !validGitRef(gitBranch) {
		return fmt.Errorf("invalid git branch name")
	}
	if buildCmd != "" {
		if err := validateBuildCommand(buildCmd); err != nil {
			return fmt.Errorf("invalid build command: %w", err)
		}
	}
	gitEnv, cloneURL, cleanupAuth, err := gitAuthEnv(gitURL, sshKeyPath, gitToken)
	if err != nil {
		return err
	}
	defer cleanupAuth()
	remoteURL := cloneURL

	gitDir := filepath.Join(def.WorkDir, ".git")
	if _, statErr := os.Stat(gitDir); os.IsNotExist(statErr) {
		if entries, err := os.ReadDir(def.WorkDir); err == nil && len(entries) > 0 {
			return fmt.Errorf("workdir %s already contains files but is not a git repo — clear it first or set git_url to match the existing repo", def.WorkDir)
		}
		args := []string{"clone"}
		if gitBranch != "" {
			args = append(args, "--branch", gitBranch, "--single-branch")
		}
		args = append(args, "--depth", "50", cloneURL, def.WorkDir)
		if err := runStep(ctx, "", "git", args, logBuf, gitEnv); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
	} else {
		if err := ensureGitOrigin(ctx, def.WorkDir, remoteURL, logBuf, gitEnv); err != nil {
			return err
		}
		if err := runStep(ctx, def.WorkDir, "git", []string{"fetch", "origin", "--depth", "50"}, logBuf, gitEnv); err != nil {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		ref := gitBranch
		if ref == "" {
			ref = resolveRemoteDefaultRef(ctx, def.WorkDir)
		} else {
			ref = "origin/" + ref
		}
		if err := runStep(ctx, def.WorkDir, "git", []string{"reset", "--hard", ref}, logBuf, nil); err != nil {
			return fmt.Errorf("git reset failed: %w", err)
		}
	}

	return runAppBuild(ctx, def, buildCmd, extraEnv, logBuf)
}

func runAppBuild(ctx context.Context, def *apps.App, buildCmd string, extraEnv map[string]string, logBuf *strings.Builder) error {
	buildCmd = strings.TrimSpace(buildCmd)
	if buildCmd == "" {
		buildCmd = detectAppBuildCmd(def.WorkDir)
	}
	if strings.EqualFold(buildCmd, "skip") || strings.EqualFold(buildCmd, "none") {
		return nil
	}
	if buildCmd == "" {
		return nil
	}
	if err := validateBuildCommand(buildCmd); err != nil {
		return fmt.Errorf("invalid build command: %w", err)
	}
	buildEnv := os.Environ()
	for k, v := range def.Env {
		buildEnv = append(buildEnv, fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range extraEnv {
		buildEnv = append(buildEnv, fmt.Sprintf("%s=%s", k, v))
	}
	logBuf.WriteString(fmt.Sprintf("\n$ %s\n", buildCmd))
	if err := runShell(ctx, def.WorkDir, buildCmd, logBuf, buildEnv); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	return nil
}

func ensureGitOrigin(ctx context.Context, workDir, gitURL string, logBuf *strings.Builder, env []string) error {
	current, err := runOutput(ctx, workDir, "git", "remote", "get-url", "origin")
	if err != nil {
		if err := runStep(ctx, workDir, "git", []string{"remote", "add", "origin", gitURL}, logBuf, env); err != nil {
			return fmt.Errorf("git remote add origin failed: %w", err)
		}
		return nil
	}
	if strings.TrimSpace(current) == gitURL {
		return nil
	}
	if err := runStep(ctx, workDir, "git", []string{"remote", "set-url", "origin", gitURL}, logBuf, env); err != nil {
		return fmt.Errorf("git remote set-url origin failed: %w", err)
	}
	return nil
}

func resolveRemoteDefaultRef(ctx context.Context, workDir string) string {
	if ref, err := runOutput(ctx, workDir, "git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(ref, "origin/") {
			return ref
		}
	}
	return "origin/HEAD"
}

// defaultGitEnv is the environment we hand to git — no interactive
// prompts (would hang) and no askpass fallback (would also hang).
func defaultGitEnv() []string {
	env := os.Environ()
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/true", "GIT_ALLOW_PROTOCOL=https:ssh:git")
	return env
}

func gitAuthEnv(gitURL, sshKeyPath, gitToken string) ([]string, string, func(), error) {
	env := defaultGitEnv()
	cleanup := func() {}
	cloneURL := strings.TrimSpace(gitURL)
	if sshKeyPath != "" {
		cleanKey := filepath.Clean(sshKeyPath)
		if !filepath.IsAbs(cleanKey) || strings.ContainsAny(cleanKey, "\x00\n\r") {
			return nil, "", cleanup, fmt.Errorf("invalid SSH key path: must be absolute")
		}
		if _, err := os.Stat(cleanKey); err != nil {
			return nil, "", cleanup, fmt.Errorf("SSH key not found: %s", cleanKey)
		}
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+shellQuote(cleanKey)+" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new")
		if gitToken == "" {
			if converted, ok := httpsGitURLToSSH(cloneURL); ok {
				cloneURL = converted
			}
		}
	}
	if gitToken != "" {
		if strings.ContainsAny(gitToken, "\x00\n\r") {
			return nil, "", cleanup, fmt.Errorf("git_token contains control characters")
		}
		if !strings.HasPrefix(strings.ToLower(gitURL), "https://") {
			return nil, "", cleanup, fmt.Errorf("git_token can only be used with https:// git URLs")
		}
		if _, err := url.Parse(gitURL); err != nil {
			return nil, "", cleanup, fmt.Errorf("invalid git_url: %w", err)
		}
		path, err := writeGitAskpass(gitToken)
		if err != nil {
			return nil, "", cleanup, err
		}
		cleanup = func() { _ = os.Remove(path) }
		env = append(env, "GIT_ASKPASS="+path)
	}
	return env, cloneURL, cleanup, nil
}

func httpsGitURLToSSH(gitURL string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(gitURL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return "", false
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	path := strings.TrimPrefix(u.EscapedPath(), "/")
	if path == "" || strings.Contains(path, "%2f") || strings.Contains(path, "%2F") {
		return "", false
	}
	host := strings.ToLower(u.Host)
	switch host {
	case "github.com", "gitlab.com", "bitbucket.org":
	default:
		return "", false
	}
	return "git@" + host + ":" + path, true
}

func writeGitAskpass(token string) (string, error) {
	f, err := os.CreateTemp("", "uwas-git-askpass-*")
	if err != nil {
		return "", fmt.Errorf("create git askpass helper: %w", err)
	}
	path := f.Name()
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"*Username*) printf '%s\\n' x-access-token ;;\n" +
		"*) printf '%s\\n' " + shellQuote(token) + " ;;\n" +
		"esac\n"
	if _, err := f.WriteString(script); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write git askpass helper: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close git askpass helper: %w", err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("chmod git askpass helper: %w", err)
	}
	return path, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func redactCommandArgs(args []string) string {
	out := make([]string, len(args))
	for i, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), "https://") && strings.Contains(arg, "@") {
			if u, err := url.Parse(arg); err == nil && u.User != nil {
				u.User = url.User("***")
				out[i] = u.String()
				continue
			}
		}
		out[i] = arg
	}
	return strings.Join(out, " ")
}

func detectAppBuildCmd(appRoot string) string {
	if data, err := os.ReadFile(filepath.Join(appRoot, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			install := "npm install"
			if _, err := os.Stat(filepath.Join(appRoot, "package-lock.json")); err == nil {
				install = "npm ci"
			} else if _, err := os.Stat(filepath.Join(appRoot, "pnpm-lock.yaml")); err == nil {
				install = "corepack pnpm install --frozen-lockfile"
			} else if _, err := os.Stat(filepath.Join(appRoot, "yarn.lock")); err == nil {
				install = "corepack yarn install --frozen-lockfile"
			}
			if _, ok := pkg.Scripts["build"]; ok {
				switch {
				case strings.HasPrefix(install, "corepack pnpm"):
					return install + " && corepack pnpm run build"
				case strings.HasPrefix(install, "corepack yarn"):
					return install + " && corepack yarn build"
				default:
					return install + " && npm run build"
				}
			}
			return install
		}
	}
	if _, err := os.Stat(filepath.Join(appRoot, "requirements.txt")); err == nil {
		return "pip install -r requirements.txt"
	}
	if _, err := os.Stat(filepath.Join(appRoot, "Gemfile")); err == nil {
		return "bundle install"
	}
	if _, err := os.Stat(filepath.Join(appRoot, "go.mod")); err == nil {
		return "go build -o main ."
	}
	return ""
}
