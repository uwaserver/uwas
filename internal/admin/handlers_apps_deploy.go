package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
)

// AppDeployRequest is the body shape for /api/v1/apps/{name}/deploy.
type AppDeployRequest struct {
	GitURL     string            `json:"git_url"`
	GitBranch  string            `json:"git_branch,omitempty"`
	BuildCmd   string            `json:"build_cmd,omitempty"`
	HealthPath string            `json:"health_path,omitempty"`
	SSHKeyPath string            `json:"ssh_key_path,omitempty"`
	GitToken   string            `json:"git_token,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
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
	OK           bool   `json:"ok"`
	Mode         string `json:"mode"` // "clone" or "pull"
	CommitSHA    string `json:"commit_sha,omitempty"`
	RolledBack   bool   `json:"rolled_back,omitempty"`
	RollbackSHA  string `json:"rollback_sha,omitempty"`
	RollbackNote string `json:"rollback_note,omitempty"`
	Log          string `json:"log"`
	Error        string `json:"error,omitempty"`
}

type AppDeployPreflightResponse struct {
	OK     bool                `json:"ok"`
	Checks []AppPreflightCheck `json:"checks"`
	App    *apps.App           `json:"app,omitempty"`
}

type AppPreflightCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Required bool   `json:"required"`
	Message  string `json:"message,omitempty"`
}

var execLookPath = exec.LookPath

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
	lock := deployLocks.get(name)
	lock.Lock()
	defer lock.Unlock()

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

	previousDeploy := def.Deploy
	previousEnv := cloneStringMap(def.Env)
	updatedDeploy := def.Deploy
	deployConfigChanged := false
	setDeployField := func(dst *string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		*dst = value
		deployConfigChanged = true
	}
	setDeployField(&updatedDeploy.GitURL, req.GitURL)
	setDeployField(&updatedDeploy.GitBranch, req.GitBranch)
	setDeployField(&updatedDeploy.BuildCmd, req.BuildCmd)
	setDeployField(&updatedDeploy.HealthPath, req.HealthPath)
	setDeployField(&updatedDeploy.SSHKeyPath, req.SSHKeyPath)
	setDeployField(&updatedDeploy.GitToken, req.GitToken)

	validationDef := *def
	validationDef.Deploy = updatedDeploy
	if err := validateDeployConfig(&validationDef); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	effectiveGitURL := strings.TrimSpace(updatedDeploy.GitURL)
	effectiveGitBranch := strings.TrimSpace(updatedDeploy.GitBranch)
	effectiveBuildCmd := strings.TrimSpace(updatedDeploy.BuildCmd)

	if err := validateGitURL(effectiveGitURL); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if effectiveGitBranch != "" && !validGitRef(effectiveGitBranch) {
		jsonError(w, "invalid git branch name", http.StatusBadRequest)
		return
	}
	if effectiveBuildCmd != "" {
		// Reuse the supervisor's allowlist so build commands can't
		// chain destructive shell metacharacters.
		if err := validateBuildCommand(effectiveBuildCmd); err != nil {
			jsonError(w, "invalid build command: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.Env != nil {
		if err := validateAppEnvMap(req.Env); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := validateDockerGitDeploy(def); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Persist the deploy config for webhook reuse after validation.
	// Only overwrite fields the operator supplied — empty fields in a
	// redeploy shouldn't wipe previously-saved settings.
	if deployConfigChanged {
		def.Deploy = updatedDeploy
		_ = s.appsMgr.Store().Save(def)
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
	startedAt := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// Determine mode before the shared core wipes the distinction.
	gitDir := filepath.Join(def.WorkDir, ".git")
	if _, statErr := os.Stat(gitDir); os.IsNotExist(statErr) {
		resp.Mode = "clone"
	} else {
		resp.Mode = "pull"
	}
	rollbackSHA := ""
	if resp.Mode == "pull" {
		rollbackSHA = currentGitSHA(ctx, def.WorkDir)
	}

	if err := runDeployCore(ctx, def, req.GitURL, req.GitBranch, req.BuildCmd, req.SSHKeyPath, req.GitToken, req.Env, logBuf); err != nil {
		resp.Error = err.Error()
		if rollbackSHA != "" {
			resp.RolledBack, resp.RollbackSHA, resp.RollbackNote = s.rollbackDeployedApp(ctx, name, def, rollbackSHA, previousDeploy, previousEnv, false, logBuf)
		}
		resp.Log = logBuf.String()
		s.recordAppDeployHistory(name, appDeployHistoryEntry{
			Source: "manual", StartedAt: startedAt, Finished: time.Now(),
			OK: false, Mode: resp.Mode, Error: resp.Error, CommitSHA: resp.CommitSHA,
			RolledBack: resp.RolledBack, RollbackSHA: resp.RollbackSHA, RollbackNote: resp.RollbackNote,
			LogTail: tailString(resp.Log, 4096),
		})
		respond500(w, &resp, resp.Log)
		return
	}
	if def.Runtime == apps.RuntimeDocker {
		logBuf.WriteString("\nDocker runtime: restarting will package the checked-out repo with docker buildx build --load.\n")
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

	if err := s.completeDeployedApp(name, def, req.SkipRestart); err != nil {
		resp.Error = err.Error()
		if rollbackSHA != "" && !req.SkipRestart {
			resp.RolledBack, resp.RollbackSHA, resp.RollbackNote = s.rollbackDeployedApp(ctx, name, def, rollbackSHA, previousDeploy, previousEnv, true, logBuf)
		}
		resp.Log = logBuf.String()
		s.recordAuditR(r, "app.deploy", name+" ("+resp.Error+")", false)
		s.recordAppDeployHistory(name, appDeployHistoryEntry{
			Source: "manual", StartedAt: startedAt, Finished: time.Now(),
			OK: false, Mode: resp.Mode, CommitSHA: resp.CommitSHA, Error: resp.Error,
			RolledBack: resp.RolledBack, RollbackSHA: resp.RollbackSHA, RollbackNote: resp.RollbackNote,
			LogTail: tailString(resp.Log, 4096),
		})
		w.WriteHeader(http.StatusOK)
		jsonResponse(w, resp)
		return
	}

	resp.OK = true
	resp.Log = logBuf.String()
	s.recordAppDeployHistory(name, appDeployHistoryEntry{
		Source: "manual", StartedAt: startedAt, Finished: time.Now(),
		OK: true, Mode: resp.Mode, CommitSHA: resp.CommitSHA, LogTail: tailString(resp.Log, 2048),
	})
	s.recordAuditR(r, "app.deploy", fmt.Sprintf("%s commit=%s", name, resp.CommitSHA), true)
	s.maybeReloadForApps()
	jsonResponse(w, resp)
}

func (s *Server) completeDeployedApp(name string, def *apps.App, skipStart bool) error {
	if def != nil {
		// Deploy intent is explicit desired state:
		// - normal deploy should ship and run, even if the app had previously
		//   been stopped (Disabled=true)
		// - skip_restart should leave it stopped across daemon restarts too
		def.Disabled = skipStart
	}
	_ = s.appsMgr.Stop(name)
	if err := s.appsMgr.Register(def); err != nil {
		return fmt.Errorf("deploy succeeded but app refresh failed: %w", err)
	}
	if skipStart {
		return nil
	}
	time.Sleep(500 * time.Millisecond)
	if err := s.appsMgr.Start(name); err != nil {
		return fmt.Errorf("deploy succeeded but restart failed: %w", err)
	}
	if err := s.appsMgr.WaitListening(name, listeningProbeTimeout); err != nil {
		return fmt.Errorf("deploy succeeded and process started, but app is not listening: %w", err)
	}
	if err := probeAppHealth(def, def.Deploy.HealthPath); err != nil {
		return fmt.Errorf("deploy succeeded and process is listening, but health check failed: %w", err)
	}
	return nil
}

func currentGitSHA(ctx context.Context, workDir string) string {
	if sha, err := runOutput(ctx, workDir, "git", "rev-parse", "HEAD"); err == nil {
		return strings.TrimSpace(sha)
	}
	return ""
}

func (s *Server) rollbackDeployedApp(
	ctx context.Context,
	name string,
	def *apps.App,
	sha string,
	deploy apps.DeployConfig,
	env map[string]string,
	restart bool,
	logBuf *strings.Builder,
) (bool, string, string) {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return false, "", "rollback skipped: previous commit is unknown"
	}
	if ctx.Err() != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ctx = rollbackCtx
	}
	def.Deploy = deploy
	def.Env = cloneStringMap(env)
	if strings.TrimSpace(deploy.GitURL) != "" {
		if err := runStep(ctx, def.WorkDir, "git", []string{"remote", "set-url", "origin", deploy.GitURL}, logBuf, nil); err != nil {
			logBuf.WriteString("Rollback warning: restore git origin failed: " + err.Error() + "\n")
		}
	}
	logBuf.WriteString(fmt.Sprintf("\nRollback: resetting %s to %s\n", name, sha))
	if err := runStep(ctx, def.WorkDir, "git", []string{"reset", "--hard", sha}, logBuf, nil); err != nil {
		return false, sha, "rollback reset failed: " + err.Error()
	}
	if err := runAppBuild(ctx, def, deploy.BuildCmd, nil, logBuf); err != nil {
		return false, sha, "rollback build failed: " + err.Error()
	}
	_ = s.appsMgr.Store().Save(def)
	if restart {
		if err := s.completeDeployedApp(name, def, false); err != nil {
			return false, sha, "rollback restart failed: " + err.Error()
		}
	}
	return true, sha, "rolled back to previous commit"
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
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := validateHealthPath(path); err != nil {
		return err
	}
	if def == nil || def.Port <= 0 {
		return fmt.Errorf("app has no HTTP port for health check")
	}
	parsedPath, err := url.ParseRequestURI(path)
	if err != nil {
		return err
	}
	u := url.URL{
		Scheme:   "http",
		Host:     fmt.Sprintf("127.0.0.1:%d", def.Port),
		Path:     parsedPath.Path,
		RawQuery: parsedPath.RawQuery,
	}
	client := &http.Client{Timeout: listeningProbeTimeout}
	resp, err := client.Get(u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("GET %s returned %s", path, resp.Status)
	}
	return nil
}

func (s *Server) handleAppDeployPreflight(w http.ResponseWriter, r *http.Request) {
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
	checks := appDeployPreflight(def)
	ok := true
	for _, check := range checks {
		if check.Required && !check.OK {
			ok = false
			break
		}
	}
	jsonResponse(w, AppDeployPreflightResponse{
		OK:     ok,
		Checks: checks,
		App:    appDefinitionForResponse(def),
	})
}

func appDeployPreflight(def *apps.App) []AppPreflightCheck {
	var checks []AppPreflightCheck
	add := func(name string, required bool, ok bool, message string) {
		checks = append(checks, AppPreflightCheck{Name: name, Required: required, OK: ok, Message: message})
	}
	hasTool := func(name string) bool {
		_, err := execLookPath(name)
		return err == nil
	}
	fileExists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}
	requireAny := func(name string, bins ...string) {
		for _, bin := range bins {
			if hasTool(bin) {
				add(name, true, true, bin)
				return
			}
		}
		add(name, true, false, "missing: "+strings.Join(bins, " or "))
	}
	requireTool := func(bin string) {
		if hasTool(bin) {
			add(bin, true, true, bin)
		} else {
			add(bin, true, false, "missing: "+bin)
		}
	}

	if def == nil {
		add("app", true, false, "missing app definition")
		return checks
	}
	if strings.TrimSpace(def.WorkDir) == "" {
		add("work_dir", true, false, "work_dir is empty")
	} else {
		add("work_dir", true, true, def.WorkDir)
	}
	if def.Deploy.GitURL != "" {
		requireTool("git")
		if err := validateDeployConfig(def); err != nil {
			add("deploy_config", true, false, err.Error())
		} else {
			add("deploy_config", true, true, "valid")
		}
	}
	if def.Deploy.SSHKeyPath != "" {
		if _, err := os.Stat(def.Deploy.SSHKeyPath); err != nil {
			add("ssh_key", true, false, err.Error())
		} else {
			add("ssh_key", true, true, def.Deploy.SSHKeyPath)
		}
	}

	switch def.Runtime {
	case apps.RuntimeNode:
		requireTool("node")
		if fileExists(filepath.Join(def.WorkDir, "package.json")) || strings.Contains(def.Deploy.BuildCmd, "npm") {
			requireTool("npm")
		}
		if fileExists(filepath.Join(def.WorkDir, "pnpm-lock.yaml")) ||
			fileExists(filepath.Join(def.WorkDir, "yarn.lock")) ||
			strings.Contains(def.Deploy.BuildCmd, "corepack") {
			requireTool("corepack")
		}
	case apps.RuntimePython:
		requireAny("python", "python3", "python")
		if fileExists(filepath.Join(def.WorkDir, "requirements.txt")) || strings.Contains(def.Deploy.BuildCmd, "pip") {
			requireAny("pip", "pip3", "pip")
		}
	case apps.RuntimeRuby:
		requireTool("ruby")
		if fileExists(filepath.Join(def.WorkDir, "Gemfile")) || strings.Contains(def.Deploy.BuildCmd, "bundle") {
			requireTool("bundle")
		}
	case apps.RuntimeGo:
		requireTool("go")
	case apps.RuntimeDocker:
		requireTool("docker")
	case apps.RuntimeCustom:
		add("runtime", false, true, "custom runtime: command is operator-managed")
	}
	for _, bin := range buildCommandTools(def.Deploy.BuildCmd) {
		requireTool(bin)
	}
	return checks
}

func buildCommandTools(command string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(command, "&&") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		bin := fields[0]
		if bin == "corepack" && len(fields) > 1 {
			if !seen["corepack"] {
				out = append(out, "corepack")
				seen["corepack"] = true
			}
			continue
		}
		if strings.Contains(bin, "/") || seen[bin] {
			continue
		}
		seen[bin] = true
		out = append(out, bin)
	}
	return out
}

func validateDockerGitDeploy(def *apps.App) error {
	if def != nil && def.Runtime == apps.RuntimeDocker && strings.TrimSpace(def.Docker.Build.Context) == "" {
		return fmt.Errorf("docker git deploy requires docker.build.context so the repo can be packaged with BuildKit")
	}
	return nil
}

func validateDeployConfig(def *apps.App) error {
	if def == nil {
		return nil
	}
	gitURL := strings.TrimSpace(def.Deploy.GitURL)
	if gitURL != "" {
		if err := validateGitURL(gitURL); err != nil {
			return err
		}
	}
	if def.Deploy.GitBranch != "" && !validGitRef(def.Deploy.GitBranch) {
		return fmt.Errorf("invalid git branch name")
	}
	if def.Deploy.BranchFilter != "" && !validGitRef(def.Deploy.BranchFilter) {
		return fmt.Errorf("invalid webhook branch filter")
	}
	if def.Deploy.BuildCmd != "" {
		if err := validateBuildCommand(def.Deploy.BuildCmd); err != nil {
			return fmt.Errorf("invalid build command: %w", err)
		}
	}
	if def.Deploy.HealthPath != "" {
		if err := validateHealthPath(def.Deploy.HealthPath); err != nil {
			return fmt.Errorf("invalid health path: %w", err)
		}
	}
	if def.Deploy.GitToken != "" {
		if strings.ContainsAny(def.Deploy.GitToken, "\x00\n\r") {
			return fmt.Errorf("git_token contains control characters")
		}
		if gitURL != "" && !strings.HasPrefix(strings.ToLower(gitURL), "https://") {
			return fmt.Errorf("git_token can only be used with https:// git URLs")
		}
	}
	if def.Deploy.SSHKeyPath != "" {
		cleanKey := filepath.Clean(def.Deploy.SSHKeyPath)
		if !filepath.IsAbs(cleanKey) || strings.ContainsAny(cleanKey, "\x00\n\r") {
			return fmt.Errorf("invalid SSH key path: must be absolute")
		}
	}
	return nil
}

func validateHealthPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("must start with /")
	}
	if strings.HasPrefix(path, "//") {
		return fmt.Errorf("must be a relative HTTP path")
	}
	if strings.ContainsAny(path, "\x00\r\n\t") {
		return fmt.Errorf("control characters not allowed")
	}
	if len(path) > 512 {
		return fmt.Errorf("too long")
	}
	if _, err := url.ParseRequestURI(path); err != nil {
		return err
	}
	return nil
}

func respond500(w http.ResponseWriter, resp *AppDeployResponse, log string) {
	resp.OK = false
	resp.Log = log
	w.WriteHeader(http.StatusOK) // deploy errors are operator-visible via resp.Error
	jsonResponse(w, resp)
}

// runStep executes a command + args (no shell), tees stdout/stderr
// into `out`, and honors the context's timeout. The `wd` argument
// chooses the working directory; empty means "current directory".
