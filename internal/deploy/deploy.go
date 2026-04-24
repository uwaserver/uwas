// Package deploy handles git-based and Docker-based application deployment.
// Supports: git clone → build → restart, Dockerfile build → container run → proxy.
package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Testable hooks — can be overridden in tests to mock command execution.
var (
	runCmdFn   = runCmdImpl
	runShellFn = runShellImpl
)

// DeployRequest describes a deployment action.
type DeployRequest struct {
	Domain      string            `json:"domain"`
	GitURL      string            `json:"git_url,omitempty"`      // e.g. https://github.com/user/repo.git
	GitBranch   string            `json:"git_branch,omitempty"`   // default: main
	BuildCmd    string            `json:"build_cmd,omitempty"`    // e.g. "npm install && npm run build"
	SSHKeyPath  string            `json:"ssh_key_path,omitempty"` // path to SSH private key for private repos
	GitToken    string            `json:"git_token,omitempty"`    // GitHub/GitLab personal access token
	DockerFile  string            `json:"dockerfile,omitempty"`   // path to Dockerfile (enables Docker mode)
	DockerPort  int               `json:"docker_port,omitempty"`  // container internal port (e.g. 3000)
	Env         map[string]string `json:"env,omitempty"`          // environment variables for build/run
}

// DeployStatus tracks a deployment.
type DeployStatus struct {
	Domain    string    `json:"domain"`
	Status    string    `json:"status"` // "deploying", "building", "running", "failed"
	GitURL    string    `json:"git_url,omitempty"`
	GitBranch string    `json:"git_branch,omitempty"`
	CommitSHA string    `json:"commit_sha,omitempty"`
	Mode      string    `json:"mode"` // "git", "docker"
	Log       string    `json:"log"`
	StartedAt time.Time `json:"started_at"`
	Duration  string    `json:"duration,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Manager handles deployments.
type Manager struct {
	mu      sync.RWMutex
	deploys map[string]*DeployStatus // domain → status
	logger  *logger.Logger
}

// New creates a deploy manager.
func New(log *logger.Logger) *Manager {
	return &Manager{
		deploys: make(map[string]*DeployStatus),
		logger:  log,
	}
}

// Deploy performs a git clone/pull → build → signals the app manager to restart.
// Returns immediately; deployment runs in background. Poll Status() for progress.
// safeGitURL rejects dangerous git transport protocols (ext::, file://) and
// ensures URLs are HTTPS, SSH, or git@ URIs only.
func safeGitURL(u string) error {
	if u == "" {
		return nil
	}
	lower := strings.ToLower(u)
	// Reject dangerous protocols
	if strings.HasPrefix(lower, "ext::") {
		return fmt.Errorf("ext:: protocol not allowed in git URLs")
	}
	if strings.HasPrefix(lower, "file://") {
		return fmt.Errorf("file:// protocol not allowed in git URLs")
	}
	// Reject command-line option injection in URLs
	if strings.Contains(lower, "--upload-pack") || strings.Contains(lower, "--receive-pack") {
		return fmt.Errorf("git option injection not allowed")
	}
	// Whitelist allowed schemes: https://, ssh://, git@
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "ssh://") && !strings.HasPrefix(lower, "git@") {
		return fmt.Errorf("only https://, ssh://, and git@ URIs are allowed")
	}
	return nil
}

// safeBranch validates a git branch name (no shell metacharacters).
func safeBranch(b string) bool {
	for _, c := range b {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '/') {
			return false
		}
	}
	return true
}

func (m *Manager) Deploy(req DeployRequest, appRoot string, onComplete func(err error)) {
	// Validate git URL — reject dangerous protocols
	if err := safeGitURL(req.GitURL); err != nil {
		if onComplete != nil {
			onComplete(err)
		}
		return
	}

	branch := req.GitBranch
	if branch == "" {
		branch = "main"
	}
	if !safeBranch(branch) {
		if onComplete != nil {
			onComplete(fmt.Errorf("invalid branch name"))
		}
		return
	}

	mode := "git"
	if req.DockerFile != "" {
		mode = "docker"
	}

	status := &DeployStatus{
		Domain:    req.Domain,
		Status:    "deploying",
		GitURL:    req.GitURL,
		GitBranch: branch,
		Mode:      mode,
		StartedAt: time.Now(),
	}

	m.mu.Lock()
	m.deploys[req.Domain] = status
	m.mu.Unlock()

	go func() {
		var log strings.Builder
		var err error

		defer func() {
			m.mu.Lock()
			status.Duration = time.Since(status.StartedAt).Truncate(time.Millisecond).String()
			status.Log = log.String()
			if err != nil {
				status.Status = "failed"
				status.Error = err.Error()
			} else {
				status.Status = "running"
			}
			m.mu.Unlock()
			if m.logger != nil {
				if err != nil {
					m.logger.Error("deploy failed", "domain", req.Domain, "error", err)
				} else {
					m.logger.Info("deploy complete", "domain", req.Domain, "duration", status.Duration)
				}
			}
			if onComplete != nil {
				onComplete(err)
			}
		}()

		if mode == "docker" {
			err = m.deployDocker(req, appRoot, status, &log)
		} else {
			err = m.deployGit(req, appRoot, branch, status, &log)
		}
	}()
}

func (m *Manager) deployGit(req DeployRequest, appRoot, branch string, status *DeployStatus, log *strings.Builder) error {
	// Step 1: Git clone or pull
	m.mu.Lock()
	status.Status = "deploying"
	m.mu.Unlock()
	gitDir := filepath.Join(appRoot, ".git")

	// Build git environment for private repo access
	gitEnv := make(map[string]string)
	for k, v := range req.Env {
		gitEnv[k] = v
	}
	// Restrict git protocols to prevent command execution via ext::
	gitEnv["GIT_ALLOW_PROTOCOL"] = "https:ssh:git"

	// SSH key auth: GIT_SSH_COMMAND with -i flag
	if req.SSHKeyPath != "" {
		// Sanitize: must be absolute, no traversal, must exist
		cleanKey := filepath.Clean(req.SSHKeyPath)
		if !filepath.IsAbs(cleanKey) || strings.Contains(cleanKey, "..") {
			return fmt.Errorf("invalid SSH key path: must be absolute")
		}
		if _, err := os.Stat(cleanKey); err != nil {
			return fmt.Errorf("SSH key not found: %s", cleanKey)
		}
		gitEnv["GIT_SSH_COMMAND"] = fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null", cleanKey)
		log.WriteString("Using SSH key: " + cleanKey + "\n")
	}

	// Token auth: rewrite HTTPS URL to include token
	gitURL := req.GitURL
	hasToken := req.GitToken != "" && gitURL != ""
	if hasToken {
		gitURL = injectTokenInURL(gitURL, req.GitToken)
		log.WriteString("Using access token for authentication\n")
	}

	// redactURL strips embedded tokens from URLs for safe logging
	redactURL := func(s string) string {
		if !hasToken || req.GitToken == "" {
			return s
		}
		return strings.ReplaceAll(s, req.GitToken, "***")
	}

	if _, err := os.Stat(gitDir); err == nil {
		// Existing repo — fetch + reset
		// If token provided, update remote URL
		if req.GitToken != "" && gitURL != "" {
			runCmd(appRoot, gitEnv, "git", "remote", "set-url", "origin", gitURL)
		}
		log.WriteString("$ git fetch origin\n")
		if out, err := runCmd(appRoot, gitEnv, "git", "fetch", "origin"); err != nil {
			return fmt.Errorf("git fetch: %w\n%s", err, redactURL(out))
		}
		log.WriteString("$ git reset --hard origin/" + branch + "\n")
		if out, err := runCmd(appRoot, gitEnv, "git", "reset", "--hard", "--", "origin/"+branch); err != nil {
			return fmt.Errorf("git reset: %w\n%s", err, redactURL(out))
		}
	} else if gitURL != "" {
		// Fresh clone
		log.WriteString("$ git clone -b " + branch + " <repo>\n")
		if out, err := runCmd(filepath.Dir(appRoot), gitEnv, "git", "clone", "-b", branch, "--", gitURL, "--", appRoot); err != nil {
			return fmt.Errorf("git clone: %w\n%s", err, redactURL(out))
		}
	} else {
		return fmt.Errorf("no git URL provided and no existing repo at %s", appRoot)
	}

	// Get commit SHA
	if sha, err := runCmd(appRoot, nil, "git", "rev-parse", "--short", "HEAD"); err == nil {
		m.mu.Lock()
		status.CommitSHA = strings.TrimSpace(sha)
		m.mu.Unlock()
		log.WriteString("Commit: " + status.CommitSHA + "\n")
	}

	// Step 2: Build
	buildCmd := req.BuildCmd
	if buildCmd == "" {
		buildCmd = detectBuildCmd(appRoot)
	}
	if buildCmd != "" {
		m.mu.Lock()
		status.Status = "building"
		m.mu.Unlock()
		log.WriteString("$ " + buildCmd + "\n")
		if out, err := runShell(appRoot, req.Env, buildCmd); err != nil {
			return fmt.Errorf("build failed: %w\n%s", err, out)
		}
		log.WriteString("Build complete\n")
	}

	return nil
}

func (m *Manager) deployDocker(req DeployRequest, appRoot string, status *DeployStatus, log *strings.Builder) error {
	containerName := "uwas-" + sanitizeName(req.Domain)
	dockerfile := req.DockerFile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	// Sanitize dockerfile path: no traversal, no absolute
	dockerfile = filepath.Clean(dockerfile)
	if filepath.IsAbs(dockerfile) || strings.HasPrefix(dockerfile, "..") {
		return fmt.Errorf("invalid Dockerfile path: must be relative within app root")
	}
	port := req.DockerPort
	if port == 0 {
		port = 3000
	}

	// Stop existing container
	log.WriteString("$ docker stop " + containerName + "\n")
	runCmd(appRoot, nil, "docker", "stop", containerName)
	runCmd(appRoot, nil, "docker", "rm", containerName)

	// Build image
	m.mu.Lock()
	status.Status = "building"
	m.mu.Unlock()
	imageName := containerName + ":latest"
	log.WriteString("$ docker build -t " + imageName + " -f " + dockerfile + " .\n")
	if out, err := runCmd(appRoot, req.Env, "docker", "build", "-t", imageName, "-f", dockerfile, "."); err != nil {
		return fmt.Errorf("docker build: %w\n%s", err, out)
	}

	// Run container
	args := []string{"run", "-d", "--name", containerName, "--restart=unless-stopped",
		"-p", fmt.Sprintf("127.0.0.1:0:%d", port)} // random host port → container port
	for k, v := range req.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, imageName)

	log.WriteString("$ docker run " + containerName + "\n")
	out, err := runCmd(appRoot, nil, "docker", args...)
	if err != nil {
		return fmt.Errorf("docker run: %w\n%s", err, out)
	}
	containerID := strings.TrimSpace(out)
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	log.WriteString("Container: " + containerID + "\n")

	// Get assigned host port
	if portOut, err := runCmd(appRoot, nil, "docker", "port", containerName, fmt.Sprintf("%d", port)); err == nil {
		log.WriteString("Listening: " + strings.TrimSpace(portOut) + "\n")
	}

	return nil
}

// Status returns the current deploy status for a domain.
func (m *Manager) Status(domain string) *DeployStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deploys[domain]
}

// AllStatuses returns all deploy statuses.
func (m *Manager) AllStatuses() []DeployStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]DeployStatus, 0, len(m.deploys))
	for _, s := range m.deploys {
		result = append(result, *s)
	}
	return result
}

// detectBuildCmd infers build commands from project files.
func detectBuildCmd(appRoot string) string {
	// Node.js
	if data, err := os.ReadFile(filepath.Join(appRoot, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			if _, ok := pkg.Scripts["build"]; ok {
				return "npm install && npm run build"
			}
			return "npm install"
		}
	}
	// Python
	if _, err := os.Stat(filepath.Join(appRoot, "requirements.txt")); err == nil {
		return "pip install -r requirements.txt"
	}
	// Ruby
	if _, err := os.Stat(filepath.Join(appRoot, "Gemfile")); err == nil {
		return "bundle install"
	}
	// Go
	if _, err := os.Stat(filepath.Join(appRoot, "go.mod")); err == nil {
		return "go build -o app ."
	}
	return ""
}

func runCmd(dir string, env map[string]string, name string, args ...string) (string, error) {
	return runCmdFn(dir, env, name, args...)
}

func runCmdImpl(dir string, env map[string]string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func runShell(dir string, env map[string]string, command string) (string, error) {
	return runShellFn(dir, env, command)
}

func runShellImpl(dir string, env map[string]string, command string) (string, error) {
	if strings.ContainsAny(command, "\x00") {
		return "", fmt.Errorf("command contains null byte")
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// injectTokenInURL rewrites https://github.com/user/repo.git
// to https://{token}@github.com/user/repo.git for private repo access.
func injectTokenInURL(gitURL, token string) string {
	if strings.HasPrefix(gitURL, "https://") {
		return "https://" + token + "@" + strings.TrimPrefix(gitURL, "https://")
	}
	if strings.HasPrefix(gitURL, "http://") {
		return "http://" + token + "@" + strings.TrimPrefix(gitURL, "http://")
	}
	return gitURL // SSH URLs don't need token injection
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else if c >= 'A' && c <= 'Z' {
			b.WriteRune(c + 32)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
