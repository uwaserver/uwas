package admin

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	deployadmin "github.com/uwaserver/uwas/internal/admin/deploy"
	"github.com/uwaserver/uwas/internal/apps"
)

// deployValidateDeployConfig delegates to the sub-package.
func deployValidateDeployConfig(def *apps.App) error {
	return deployadmin.ValidateDeployConfigExported(def)
}

// deployDeps adapts admin.Server to the deploy.Deps interface.
type deployDeps struct {
	s *Server
}

func (d *deployDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}
func (d *deployDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requirePin(w, r)
}
func (d *deployDeps) LogInfo(msg string, args ...any)  { d.s.logger.Info(msg, args...) }
func (d *deployDeps) LogWarn(msg string, args ...any)  { d.s.logger.Warn(msg, args...) }
func (d *deployDeps) LogError(msg string, args ...any) { d.s.logger.Error(msg, args...) }
func (d *deployDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}
func (d *deployDeps) AppsManager() *apps.Manager { return d.s.appsMgr }
func (d *deployDeps) ConfigPath() string         { return d.s.configPath }
func (d *deployDeps) ValidateDeployConfig(def *apps.App) error {
	if err := deployValidateDeployConfig(def); err != nil {
		return err
	}
	return validateAppEnvMap(def.Env)
}
func (d *deployDeps) Reload() error {
	if d.s.reloadFn == nil {
		return nil
	}
	return d.s.reloadFn()
}
func (d *deployDeps) AppCompleteDeploy(name string, def *apps.App, skipStart bool) error {
	return d.s.completeDeployedApp(name, def, skipStart)
}
func (d *deployDeps) AppRollback(ctx context.Context, name string, def *apps.App, rollbackSHA string, deployCfg apps.DeployConfig, env map[string]string, restart bool, logBuf *strings.Builder) (bool, string, string) {
	return d.s.rollbackDeployedApp(ctx, name, def, rollbackSHA, deployCfg, env, restart, logBuf)
}

// deployHandler holds the deploy admin handler instance.
var deployHandler *deployadmin.Handler

func (s *Server) initDeployHandler() {
	deployHandler = deployadmin.New(&deployDeps{s: s})
}

// ── Thin wrappers ──

func (s *Server) handleAppDeploy(w http.ResponseWriter, r *http.Request)           { deployHandler.Deploy(w, r) }
func (s *Server) handleAppDeployPreflight(w http.ResponseWriter, r *http.Request)   { deployHandler.DeployPreflight(w, r) }
func (s *Server) handleAppWebhook(w http.ResponseWriter, r *http.Request)           { deployHandler.Webhook(w, r) }
func (s *Server) handleAppWebhookStatus(w http.ResponseWriter, r *http.Request)     { deployHandler.WebhookStatus(w, r) }
func (s *Server) handleAppDeployHistory(w http.ResponseWriter, r *http.Request)     { deployHandler.DeployHistory(w, r) }
func (s *Server) handleAppGenerateDeployKey(w http.ResponseWriter, r *http.Request) { deployHandler.GenerateDeployKey(w, r) }

// Compile-time check.
var _ deployadmin.Deps = (*deployDeps)(nil)

// Retained git helpers for test compat.
var (
	_ = deployadmin.ValidateDeployConfigExported
)

// buildShellCmd delegates to the deploy sub-package.
func buildShellCmd(ctx context.Context, command string) *exec.Cmd {
	return deployadmin.BuildShellCmdExported(ctx, command)
}

// httpsGitURLToSSH delegates to the deploy sub-package.
func httpsGitURLToSSH(gitURL string) (string, bool) {
	return deployadmin.HTTPSGitURLToSSH(gitURL)
}

// validGitRef delegates to the deploy sub-package.
func validGitRef(s string) bool { return deployadmin.ValidGitRef(s) }

// validateGitURL delegates to the deploy sub-package.
func validateGitURL(u string) error { return deployadmin.ValidateGitURL(u) }

// validateBuildCommand delegates to the deploy sub-package.
func validateBuildCommand(s string) error { return deployadmin.ValidateBuildCommand(s) }

// redactCommandArgs delegates to the deploy sub-package.
func redactCommandArgs(args []string) string { return deployadmin.RedactCommandArgs(args) }

// detectAppBuildCmd delegates to the deploy sub-package.
func detectAppBuildCmd(appRoot string) string { return deployadmin.DetectAppBuildCmd(appRoot) }

// gitAuthEnv delegates to the deploy sub-package.
func gitAuthEnv(gitURL, sshKeyPath, gitToken string) ([]string, string, func(), error) {
	return deployadmin.GitAuthEnv(gitURL, sshKeyPath, gitToken)
}

// writeGitAskpass delegates to the deploy sub-package.
func writeGitAskpass(token string) (string, error) { return deployadmin.WriteGitAskpass(token) }

// shellQuote delegates to the deploy sub-package.
func shellQuote(s string) string { return deployadmin.ShellQuote(s) }

// runStep delegates to the deploy sub-package.
func runStep(ctx context.Context, wd, name string, args []string, out *strings.Builder, env []string) error {
	return deployadmin.RunStep(ctx, wd, name, args, out, env)
}

// runOutput delegates to the deploy sub-package.
func runOutput(ctx context.Context, wd, name string, args ...string) (string, error) {
	return deployadmin.RunOutput(ctx, wd, name, args...)
}

// runShell delegates to the deploy sub-package.
func runShell(ctx context.Context, wd, command string, out *strings.Builder, env []string) error {
	return deployadmin.RunShell(ctx, wd, command, out, env)
}

// isWindows delegates to the deploy sub-package.
func isWindows() bool { return deployadmin.IsWindows() }

// tailString delegates to the deploy sub-package.
func tailString(s string, n int) string { return deployadmin.TailString(s, n) }

// validateDockerGitDeploy delegates to the deploy sub-package.
func validateDockerGitDeploy(def *apps.App) error { return deployadmin.ValidateDockerGitDeploy(def) }

// ensureGitOrigin delegates to the deploy sub-package.
func ensureGitOrigin(ctx context.Context, workDir, gitURL string, logBuf *strings.Builder, env []string) error {
	return deployadmin.EnsureGitOrigin(ctx, workDir, gitURL, logBuf, env)
}

// probeAppHealth delegates to the deploy sub-package.
func probeAppHealth(def *apps.App, path string) error { return deployadmin.ProbeAppHealth(def, path) }

// Retained types for test compat.
type AppDeployKeyResponse = deployadmin.AppDeployKeyResponse
type AppPreflightCheck = deployadmin.AppPreflightCheck
type AppDeployResponse = deployadmin.AppDeployResponse

// Retained webhook state for test compat.
var (
	lastWebhookMu     sync.Mutex
	lastWebhookByName = make(map[string]*deployadmin.WebhookDeployStatus)
)

func (s *Server) runWebhookDeploy(name, ref string) {
	deployHandler.RunWebhookDeploy(name, ref)
}

// appDeployHistoryEntry is aliased for test compat.
type appDeployHistoryEntry = deployadmin.DeployHistoryEntry

// persistAppDeployHistory delegates to the sub-package.
func persistAppDeployHistory(root, name string, items []deployadmin.DeployHistoryEntry) error {
	return deployadmin.PersistDeployHistoryExported(root, name, items)
}

// loadAppDeployHistory delegates to the sub-package.
func loadAppDeployHistory(root, name string) []deployadmin.DeployHistoryEntry {
	return deployadmin.LoadDeployHistoryExported(root, name)
}

// runDeployCore delegates to the sub-package.
func runDeployCore(ctx context.Context, def *apps.App, gitURL, branch, buildCmd, sshKeyPath, gitToken string, extraEnv map[string]string, logBuf *strings.Builder) error {
	return deployadmin.RunDeployCoreExported(ctx, def, gitURL, branch, buildCmd, sshKeyPath, gitToken, extraEnv, logBuf)
}

// verifyWebhookSignature delegates to the sub-package.
func verifyWebhookSignature(r *http.Request, body []byte, secret string) bool {
	return deployadmin.VerifyWebhookSignature(r, body, secret)
}

// extractPushRef delegates to the sub-package.
func extractPushRef(body []byte) string { return deployadmin.ExtractPushRef(body) }

// generateAppDeployKeyFn delegates to the sub-package.
func generateAppDeployKeyFn(storeDir, appName string) (string, string, error) {
	return deployadmin.GenerateAppDeployKey(storeDir, appName)
}

// execLookPath delegates to os/exec for test compat.
var execLookPath = exec.LookPath

// appDeployPreflight delegates to the sub-package.
func appDeployPreflight(def *apps.App) []AppPreflightCheck {
	return deployadmin.AppDeployPreflight(def)
}

// deployHistoryMu and deployHistory are retained for test compat.
var deployHistoryMu sync.Mutex
var deployHistory = make(map[string][]appDeployHistoryEntry)

// deployLockMap is a per-app mutex map (retained for test compat).
type deployLockMap struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (d *deployLockMap) get(name string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.locks == nil {
		d.locks = make(map[string]*sync.Mutex)
	}
	m, ok := d.locks[name]
	if !ok {
		m = &sync.Mutex{}
		d.locks[name] = m
	}
	return m
}

var deployLocks = &deployLockMap{locks: make(map[string]*sync.Mutex)}

// recordAppDeployHistory delegates to the sub-package.
func recordAppDeployHistory(name string, entry appDeployHistoryEntry) []appDeployHistoryEntry {
	deployHistoryMu.Lock()
	items := append([]appDeployHistoryEntry{entry}, deployHistory[name]...)
	if len(items) > 20 {
		items = items[:20]
	}
	deployHistory[name] = items
	deployHistoryMu.Unlock()
	return items
}

// validateHealthPath delegates to the sub-package.
func validateHealthPath(path string) error { return deployadmin.ValidateHealthPath(path) }

// deployHistoryPath delegates to the sub-package.
func deployHistoryPath(root, name string) string { return deployadmin.DeployHistoryPath(root, name) }

// Retained functions for other admin files.
func validateDeployConfig(def *apps.App) error {
	return deployValidateDeployConfig(def)
}

func (s *Server) completeDeployedApp(name string, def *apps.App, skipStart bool) error {
	if s.appsMgr == nil {
		return nil
	}
	if skipStart {
		return nil
	}
	return s.appsMgr.Restart(name)
}

func (s *Server) rollbackDeployedApp(
	ctx context.Context,
	name string,
	def *apps.App,
	rollbackSHA string,
	deployCfg apps.DeployConfig,
	env map[string]string,
	restart bool,
	logBuf *strings.Builder,
) (bool, string, string) {
	if rollbackSHA == "" {
		return false, "", "no rollback SHA"
	}
	_ = s.appsMgr.Stop(name)
	// Run git reset --hard <sha> via exec
	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", rollbackSHA)
	cmd.Dir = def.WorkDir
	var combined strings.Builder
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		if logBuf != nil {
			logBuf.WriteString(combined.String())
		}
		return false, "", "git reset failed: " + err.Error()
	}
	if logBuf != nil {
		logBuf.WriteString(combined.String())
	}
	if restart {
		if err := s.appsMgr.Restart(name); err != nil {
			return false, rollbackSHA, "restart failed: " + err.Error()
		}
	}
	return true, rollbackSHA[:7], "rolled back to " + rollbackSHA[:7]
}
