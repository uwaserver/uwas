package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
)

// AppDeployRequest is the body shape for /api/v1/apps/{name}/deploy.
type AppDeployRequest struct {
	GitURL    string            `json:"git_url"`
	GitBranch string            `json:"git_branch,omitempty"`
	BuildCmd  string            `json:"build_cmd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	// SkipRestart leaves the app stopped after deploy. Default false:
	// a deploy means "ship a new version", so the supervisor restarts
	// to pick it up.
	SkipRestart bool `json:"skip_restart,omitempty"`
}

// AppDeployResponse describes the outcome of a synchronous
// git-deploy. The Log field carries the merged stdout/stderr from
// every git/build step so the dashboard can render the same view
// `docker logs` would offer.
type AppDeployResponse struct {
	OK        bool   `json:"ok"`
	Mode      string `json:"mode"` // "clone" or "pull"
	CommitSHA string `json:"commit_sha,omitempty"`
	Log       string `json:"log"`
	Error     string `json:"error,omitempty"`
}

// handleAppDeploy clones (or pulls) a git repo into the app's
// workdir, optionally runs a build command, then triggers a restart.
//
// Runs synchronously with a 5-minute hard cap. Async multi-stage
// deploys can be layered on later, but for a typical small project
// `git clone + npm install` finishes well inside that budget and the
// "click → see result" UX is much better than polling.
//
// Safety:
//   - URL scheme whitelisted to https://, ssh://, git@
//   - Branch name validated against shell-injection patterns
//   - Build command goes through validateShellCommand (already used
//     by the supervisor for the start command)
//   - Working directory is exclusively the app's workdir — no
//     directory traversal via the request.
//
// Side effect: the request's git_url / git_branch / build_cmd
// (whatever is non-empty) are persisted to App.Deploy so subsequent
// webhook-triggered deploys can reuse them without operator input.
func (s *Server) handleAppDeploy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}

	name := r.PathValue("name")
	def, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req AppDeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Persist the deploy config for webhook reuse. Only overwrite
	// fields the operator supplied — empty git_branch in a redeploy
	// shouldn't wipe a previously-saved branch.
	if req.GitURL != "" || req.GitBranch != "" || req.BuildCmd != "" {
		if req.GitURL != "" {
			def.Deploy.GitURL = req.GitURL
		}
		if req.GitBranch != "" {
			def.Deploy.GitBranch = req.GitBranch
		}
		if req.BuildCmd != "" {
			def.Deploy.BuildCmd = req.BuildCmd
		}
		_ = s.appsMgr.Store().Save(def)
	}

	if err := validateGitURL(req.GitURL); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.GitBranch != "" && !validGitRef(req.GitBranch) {
		jsonError(w, "invalid git branch name", http.StatusBadRequest)
		return
	}
	if req.BuildCmd != "" {
		// Reuse the supervisor's allowlist so build commands can't
		// chain destructive shell metacharacters.
		if err := validateBuildCommand(req.BuildCmd); err != nil {
			jsonError(w, "invalid build command: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if def.WorkDir == "" {
		jsonError(w, "app has no work_dir resolved", http.StatusInternalServerError)
		return
	}

	// Ensure the parent of workdir exists (workdir itself is created
	// by `git clone` for fresh deploys; for `git pull` it must
	// already exist with a .git/).
	if err := os.MkdirAll(filepath.Dir(def.WorkDir), 0755); err != nil {
		jsonError(w, "create workdir parent: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := AppDeployResponse{}
	logBuf := &strings.Builder{}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// Determine mode before the shared core wipes the distinction.
	gitDir := filepath.Join(def.WorkDir, ".git")
	if _, statErr := os.Stat(gitDir); os.IsNotExist(statErr) {
		resp.Mode = "clone"
	} else {
		resp.Mode = "pull"
	}

	if err := runDeployCore(ctx, def, req.GitURL, req.GitBranch, req.BuildCmd, req.Env, logBuf); err != nil {
		resp.Error = err.Error()
		respond500(w, &resp, logBuf.String())
		return
	}

	// Capture commit SHA for the response (operator-visible audit trail).
	if sha, err := runOutput(ctx, def.WorkDir, "git", "rev-parse", "HEAD"); err == nil {
		resp.CommitSHA = strings.TrimSpace(sha)
	}

	// Persist any env updates the deploy request introduced — operators
	// commonly pass new build-time vars and expect them to stick for
	// the next deploy.
	if len(req.Env) > 0 {
		if def.Env == nil {
			def.Env = make(map[string]string)
		}
		for k, v := range req.Env {
			def.Env[k] = v
		}
		_ = s.appsMgr.Store().Save(def)
	}

	if !req.SkipRestart {
		if err := s.appsMgr.Restart(name); err != nil {
			resp.Error = "deploy succeeded but restart failed: " + err.Error()
			resp.Log = logBuf.String()
			s.recordAuditR(r, "app.deploy", name+" (restart failed)", false)
			w.WriteHeader(http.StatusOK) // still 200 — code IS deployed
			jsonResponse(w, resp)
			return
		}
		// Verify the new code is actually listening. A deploy that
		// fast-forwards bad code (e.g. crashes on import) would
		// otherwise be reported as "deploy ok" while the proxy 502s
		// against the dead process.
		if err := s.appsMgr.WaitListening(name, listeningProbeTimeout); err != nil {
			resp.Error = "deploy succeeded and process started, but app is not listening: " + err.Error()
			resp.Log = logBuf.String()
			s.recordAuditR(r, "app.deploy", name+" (not listening)", false)
			w.WriteHeader(http.StatusOK)
			jsonResponse(w, resp)
			return
		}
	}

	resp.OK = true
	resp.Log = logBuf.String()
	s.recordAuditR(r, "app.deploy", fmt.Sprintf("%s commit=%s", name, resp.CommitSHA), true)
	s.maybeReloadForApps()
	jsonResponse(w, resp)
}

func respond500(w http.ResponseWriter, resp *AppDeployResponse, log string) {
	resp.OK = false
	resp.Log = log
	w.WriteHeader(http.StatusOK) // deploy errors are operator-visible via resp.Error
	jsonResponse(w, resp)
}

// gitEnv builds the environment for a git command, injecting an SSH
// command override when a private key is needed. For now we don't
// support per-deploy SSH keys — operators with private repos use the
// server's deploy key in ~root/.ssh/. This hook is here for the
// follow-up that adds per-app credentials.
func gitEnv(_ AppDeployRequest) []string {
	env := os.Environ()
	// Non-interactive: never prompt for credentials. A repo that
	// needs auth and doesn't have it should fail fast, not hang.
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
	)
	return env
}

// runStep executes a command + args (no shell), tees stdout/stderr
// into `out`, and honors the context's timeout. The `wd` argument
// chooses the working directory; empty means "current directory".
func runStep(ctx context.Context, wd, name string, args []string, out *strings.Builder, env []string) error {
	out.WriteString(fmt.Sprintf("\n$ %s %s\n", name, strings.Join(args, " ")))
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
	gitURL, gitBranch, buildCmd string,
	extraEnv map[string]string,
	logBuf *strings.Builder,
) error {
	if def == nil || def.WorkDir == "" {
		return fmt.Errorf("app has no work_dir resolved")
	}
	if err := os.MkdirAll(filepath.Dir(def.WorkDir), 0755); err != nil {
		return fmt.Errorf("create workdir parent: %w", err)
	}

	gitDir := filepath.Join(def.WorkDir, ".git")
	if _, statErr := os.Stat(gitDir); os.IsNotExist(statErr) {
		if entries, err := os.ReadDir(def.WorkDir); err == nil && len(entries) > 0 {
			return fmt.Errorf("workdir %s already contains files but is not a git repo — clear it first or set git_url to match the existing repo", def.WorkDir)
		}
		args := []string{"clone"}
		if gitBranch != "" {
			args = append(args, "--branch", gitBranch, "--single-branch")
		}
		args = append(args, "--depth", "50", gitURL, def.WorkDir)
		if err := runStep(ctx, "", "git", args, logBuf, defaultGitEnv()); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
	} else {
		if err := runStep(ctx, def.WorkDir, "git", []string{"fetch", "origin", "--depth", "50"}, logBuf, defaultGitEnv()); err != nil {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		ref := gitBranch
		if ref == "" {
			ref = "HEAD"
		} else {
			ref = "origin/" + ref
		}
		if err := runStep(ctx, def.WorkDir, "git", []string{"reset", "--hard", ref}, logBuf, nil); err != nil {
			return fmt.Errorf("git reset failed: %w", err)
		}
	}

	if buildCmd != "" {
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
	}
	return nil
}

// defaultGitEnv is the environment we hand to git — no interactive
// prompts (would hang) and no askpass fallback (would also hang).
// Same as gitEnv() but doesn't require a AppDeployRequest.
func defaultGitEnv() []string {
	env := os.Environ()
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/true")
	return env
}
