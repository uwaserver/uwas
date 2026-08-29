package deploy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
	"golang.org/x/crypto/ssh"
)

// runStep runs a command and tees output to the log buffer.
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

func runOutput(ctx context.Context, wd, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = wd
	out, err := cmd.Output()
	return string(out), err
}

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

func buildShellCmd(ctx context.Context, command string) *exec.Cmd {
	if isWindows() {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}

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

func defaultGitEnv() []string {
	env := os.Environ()
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=https:ssh:git")

	// A no-op askpass keeps git from popping a GUI credential prompt when the
	// environment has SSH_ASKPASS set; GIT_TERMINAL_PROMPT=0 only covers the
	// terminal. The path was hardcoded to /bin/true, which does not exist on
	// macOS (true lives in /usr/bin) or in some minimal images — and git
	// answers a missing GIT_ASKPASS with "cannot run ...", which is worse
	// than not setting it. Resolve it, or leave it out.
	if p := noopBinary(); p != "" {
		env = append(env, "GIT_ASKPASS="+p)
	}
	return env
}

// noopBinary locates a binary that exits successfully and prints nothing.
// Resolved once: it cannot change while the process runs.
var noopBinary = sync.OnceValue(func() string {
	if p, err := exec.LookPath("true"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/bin/true", "/bin/true"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
})

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
		// Replace, not append: a second GIT_ASKPASS= entry leaves the no-op
		// helper in the environment behind the real one. exec resolves
		// duplicates last-wins so it worked, but anything reading the slice
		// sees a stale entry pointing at a binary that answers nothing.
		env = setEnv(env, "GIT_ASKPASS", path)
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

func currentGitSHA(ctx context.Context, workDir string) string {
	sha, err := runOutput(ctx, workDir, "git", "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sha)
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func probeAppHealth(def *apps.App, path string) error {
	if def == nil {
		return fmt.Errorf("HTTP port not set (nil app)")
	}
	if def.Port == 0 {
		return fmt.Errorf("HTTP port not set")
	}
	if path == "" {
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	addr := fmt.Sprintf("127.0.0.1:%d", def.Port)
	url := fmt.Sprintf("http://%s%s", addr, path)
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}

func appDeployPreflight(def *apps.App) []AppPreflightCheck {
	var checks []AppPreflightCheck
	// Docker check
	if def.Docker.Build.Dockerfile != "" {
		checks = append(checks, AppPreflightCheck{
			Name: "docker", Label: "Docker Compose", OK: true, Detail: "Docker-based app",
		})
	} else {
		// Port check
		checks = append(checks, AppPreflightCheck{
			Name: "port", Label: "Port " + fmt.Sprintf("%d", def.Port), OK: def.Port > 0,
		})
		// Workdir check
		if def.WorkDir != "" {
			if _, err := os.Stat(def.WorkDir); err == nil {
				checks = append(checks, AppPreflightCheck{
					Name: "workdir", Label: "Workdir exists", OK: true,
				})
			} else {
				checks = append(checks, AppPreflightCheck{
					Name: "workdir", Label: "Workdir exists", OK: false, Detail: def.WorkDir + " not found (will be created)",
				})
			}
		}
	}
	// Git URL check
	if def.Deploy.GitURL != "" {
		checks = append(checks, AppPreflightCheck{Name: "deploy", Label: "Deploy Config", OK: true, Detail: def.Deploy.GitURL})
	}
	return checks
}

func validateDockerGitDeploy(def *apps.App) error {
	if def == nil {
		return nil
	}
	if def.Runtime != apps.RuntimeDocker {
		return nil
	}
	if def.Docker.Build.Context == "" {
		return fmt.Errorf("docker git deploy requires docker.build.context")
	}
	return nil
}

func CloneStringMap(in map[string]string) map[string]string { return cloneStringMap(in) }

func validateHealthPath(path string) error {
	if path == "" {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("health path must start with /")
	}
	if strings.HasPrefix(path, "//") {
		return fmt.Errorf("health path cannot start with //")
	}
	if strings.ContainsAny(path, "\n\r") {
		return fmt.Errorf("health path contains newline")
	}
	if len(path) > 512 {
		return fmt.Errorf("health path too long")
	}
	return nil
}

// ValidateDeployConfigExported is the exported validation function.
func ValidateDeployConfigExported(def *apps.App) error {
	return validateDeployConfigImpl(def)
}

func validateDeployConfigImpl(def *apps.App) error {
	if def == nil {
		return nil
	}
	if def.Deploy.GitURL != "" {
		if err := validateGitURL(def.Deploy.GitURL); err != nil {
			return err
		}
	}
	if def.Deploy.GitBranch != "" && !validGitRef(def.Deploy.GitBranch) {
		return fmt.Errorf("invalid git branch name")
	}
	if def.Deploy.BranchFilter != "" && !validGitRef(def.Deploy.BranchFilter) {
		return fmt.Errorf("invalid branch filter")
	}
	if def.Deploy.BuildCmd != "" {
		if err := validateBuildCommand(def.Deploy.BuildCmd); err != nil {
			return fmt.Errorf("invalid build command: %w", err)
		}
	}
	if def.Deploy.SSHKeyPath != "" {
		if strings.ContainsAny(def.Deploy.SSHKeyPath, "\x00") {
			return fmt.Errorf("invalid SSH key path: null byte in path")
		}
		cleanKey := filepath.Clean(def.Deploy.SSHKeyPath)
		if !filepath.IsAbs(cleanKey) {
			return fmt.Errorf("invalid SSH key path: path must be absolute")
		}
	}
	if def.Deploy.GitToken != "" {
		if strings.ContainsAny(def.Deploy.GitToken, "\x00\n\r") {
			return fmt.Errorf("git_token contains control characters")
		}
		if !strings.HasPrefix(strings.ToLower(def.Deploy.GitURL), "https://") {
			return fmt.Errorf("git_token can only be used with https:// git URLs")
		}
	}
	if def.Deploy.HealthPath != "" {
		if err := validateHealthPath(def.Deploy.HealthPath); err != nil {
			return err
		}
	}
	return nil
}

func tailString(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[len(s)-n:])
}

func verifyWebhookSignature(r *http.Request, body []byte, secret string) bool {
	if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
		const prefix = "sha256="
		if !strings.HasPrefix(sig, prefix) {
			return false
		}
		expected := hmac.New(sha256.New, []byte(secret))
		expected.Write(body)
		want := hex.EncodeToString(expected.Sum(nil))
		got := sig[len(prefix):]
		return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
	}
	if token := r.Header.Get("X-Gitlab-Token"); token != "" {
		return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
	}
	return false
}

// webhookPayloadJSON returns the JSON document out of a webhook request body.
//
// GitHub sends the payload one of two ways depending on the webhook's content
// type setting. With application/json the body *is* the JSON. With
// application/x-www-form-urlencoded — GitHub's default, and the one most
// operators never change — the body is a form whose "payload" field holds the
// JSON. Reading that form as JSON yields no ref, which would turn a push into
// a deploy with an empty ref and defeat branch filtering.
//
// The HMAC is deliberately not computed from this: GitHub signs the raw body
// it sent, so verifyWebhookSignature must keep using the untouched bytes.
func webhookPayloadJSON(r *http.Request, body []byte) []byte {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if strings.TrimSpace(ct) != "application/x-www-form-urlencoded" {
		return body
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return body
	}
	if payload := values.Get("payload"); payload != "" {
		return []byte(payload)
	}
	return body
}

func extractPushRef(body []byte) string {
	var payload struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Ref
}

func deployHistoryPath(root, name string) string {
	if name == "" || name != filepath.Base(name) {
		return ""
	}
	return filepath.Join(root, ".deploy-history", name+".json")
}

func persistDeployHistory(root, name string, items []DeployHistoryEntry) error {
	if root == "" || name == "" {
		return nil
	}
	if len(items) > 20 {
		items = items[:20]
	}
	dir := filepath.Join(root, ".deploy-history")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	path := deployHistoryPath(root, name)
	if path == "" {
		return nil
	}
	tmp, err := os.CreateTemp(dir, name+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.WriteString("\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

func loadDeployHistory(root, name string) []DeployHistoryEntry {
	if root == "" || name == "" {
		return nil
	}
	path := deployHistoryPath(root, name)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var items []DeployHistoryEntry
	if err := json.Unmarshal(data, &items); err != nil {
		return nil
	}
	if len(items) > 20 {
		items = items[:20]
	}
	return items
}

// Exported wrappers for admin adapter test compat.
func BuildShellCmdExported(ctx context.Context, command string) *exec.Cmd {
	return buildShellCmd(ctx, command)
}
func HTTPSGitURLToSSH(gitURL string) (string, bool) { return httpsGitURLToSSH(gitURL) }
func ValidGitRef(s string) bool                     { return validGitRef(s) }
func ValidateGitURL(u string) error                 { return validateGitURL(u) }
func ValidateBuildCommand(s string) error           { return validateBuildCommand(s) }
func RedactCommandArgs(args []string) string        { return redactCommandArgs(args) }
func DetectAppBuildCmd(appRoot string) string       { return detectAppBuildCmd(appRoot) }
func GitAuthEnv(gitURL, sshKeyPath, gitToken string) ([]string, string, func(), error) {
	return gitAuthEnv(gitURL, sshKeyPath, gitToken)
}
func WriteGitAskpass(token string) (string, error) { return writeGitAskpass(token) }
func ShellQuote(s string) string                   { return shellQuote(s) }
func RunStep(ctx context.Context, wd, name string, args []string, out *strings.Builder, env []string) error {
	return runStep(ctx, wd, name, args, out, env)
}
func RunOutput(ctx context.Context, wd, name string, args ...string) (string, error) {
	return runOutput(ctx, wd, name, args...)
}
func RunShell(ctx context.Context, wd, command string, out *strings.Builder, env []string) error {
	return runShell(ctx, wd, command, out, env)
}
func IsWindows() bool                             { return isWindows() }
func TailString(s string, n int) string           { return tailString(s, n) }
func ValidateDockerGitDeploy(def *apps.App) error { return validateDockerGitDeploy(def) }
func EnsureGitOrigin(ctx context.Context, workDir, gitURL string, logBuf *strings.Builder, env []string) error {
	return ensureGitOrigin(ctx, workDir, gitURL, logBuf, env)
}
func ProbeAppHealth(def *apps.App, path string) error { return probeAppHealth(def, path) }

// Additional exported wrappers for admin adapter test compat.
func PersistDeployHistoryExported(root, name string, items []DeployHistoryEntry) error {
	return persistDeployHistory(root, name, items)
}
func LoadDeployHistoryExported(root, name string) []DeployHistoryEntry {
	return loadDeployHistory(root, name)
}
func RunDeployCoreExported(ctx context.Context, def *apps.App, gitURL, branch, buildCmd, sshKeyPath, gitToken string, extraEnv map[string]string, logBuf *strings.Builder) error {
	return runDeployCore(ctx, def, gitURL, branch, buildCmd, sshKeyPath, gitToken, extraEnv, logBuf)
}
func VerifyWebhookSignature(r *http.Request, body []byte, secret string) bool {
	return verifyWebhookSignature(r, body, secret)
}
func ExtractPushRef(body []byte) string { return extractPushRef(body) }
func GenerateAppDeployKey(storeDir, appName string) (string, string, error) {
	return generateAppDeployKey(storeDir, appName)
}
func AppDeployPreflight(def *apps.App) []AppPreflightCheck { return appDeployPreflight(def) }
func ValidateHealthPath(path string) error                 { return validateHealthPath(path) }
func DeployHistoryPath(root, name string) string           { return deployHistoryPath(root, name) }

func generateAppDeployKey(storeDir, appName string) (string, string, error) {
	return generateDeployKeyImpl(storeDir, appName)
}

func generateDeployKeyImpl(storeDir, appName string) (string, string, error) {
	if storeDir == "" {
		return "", "", fmt.Errorf("apps store directory is not configured")
	}
	keyDir := filepath.Join(storeDir, "deploy-keys", appName)
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return "", "", fmt.Errorf("create deploy key dir: %w", err)
	}
	if err := os.Chmod(keyDir, 0700); err != nil {
		return "", "", fmt.Errorf("chmod deploy key dir: %w", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 deploy key: %w", err)
	}
	privateBlock, err := ssh.MarshalPrivateKey(priv, "uwas "+appName+" deploy key")
	if err != nil {
		return "", "", fmt.Errorf("marshal deploy private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("marshal deploy public key: %w", err)
	}
	privatePath := filepath.Join(keyDir, "id_ed25519")
	tmp := privatePath + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(privateBlock), 0600); err != nil {
		return "", "", fmt.Errorf("write deploy private key: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("chmod deploy private key: %w", err)
	}
	if err := os.Rename(tmp, privatePath); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("install deploy private key: %w", err)
	}
	return privatePath, string(ssh.MarshalAuthorizedKey(sshPub)), nil
}

// setEnv replaces KEY=... in an environment slice, appending if absent.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
