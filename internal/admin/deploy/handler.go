// Package deploy provides admin API handlers for application deployment:
// manual deploy, webhook-triggered auto-deploy, deploy history, and
// deploy key generation. Extracted from the admin package following the
// established Deps-interface pattern.
package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	// Logging
	LogInfo(msg string, args ...any)
	LogWarn(msg string, args ...any)
	LogError(msg string, args ...any)
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
	// Apps manager
	AppsManager() *apps.Manager
	// Config
	ConfigPath() string
	// Validation
	ValidateDeployConfig(def *apps.App) error
	// Reload
	Reload() error
	// App lifecycle (for post-deploy restart)
	AppCompleteDeploy(name string, def *apps.App, skipStart bool) error
	AppRollback(ctx context.Context, name string, def *apps.App, rollbackSHA string, deployCfg apps.DeployConfig, env map[string]string, restart bool, logBuf *strings.Builder) (bool, string, string)
}

// ── Types ──

// AppDeployResponse is returned by the deploy handler.
type AppDeployResponse struct {
	OK           bool   `json:"ok"`
	Message      string `json:"message,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	Error        string `json:"error,omitempty"`
	Log          string `json:"log,omitempty"` // legacy field for test compat
	LogTail      string `json:"log_tail,omitempty"`
	RolledBack   bool   `json:"rolled_back,omitempty"`
	RollbackSHA  string `json:"rollback_sha,omitempty"`
	RollbackNote string `json:"rollback_note,omitempty"`
}

// AppPreflightCheck is a single preflight check result.
type AppPreflightCheck struct {
	Name     string `json:"name,omitempty"`
	Label    string `json:"label"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// AppDeployKeyResponse is returned by the deploy key handler.
type AppDeployKeyResponse struct {
	PrivateKeyPath string `json:"private_key_path"`
	PublicKey      string `json:"public_key"`
}

// WebhookDeployStatus tracks the most recent webhook deploy outcome.
type WebhookDeployStatus struct {
	StartedAt    time.Time `json:"started_at"`
	Finished     time.Time `json:"finished_at,omitempty"`
	OK           bool      `json:"ok"`
	CommitSHA    string    `json:"commit_sha,omitempty"`
	Ref          string    `json:"ref,omitempty"`
	RolledBack   bool      `json:"rolled_back,omitempty"`
	RollbackSHA  string    `json:"rollback_sha,omitempty"`
	RollbackNote string    `json:"rollback_note,omitempty"`
	Error        string    `json:"error,omitempty"`
	LogTail      string    `json:"log_tail,omitempty"`
}

// DeployHistoryEntry is a single deploy history record.
type DeployHistoryEntry struct {
	Source       string    `json:"source"`
	StartedAt    time.Time `json:"started_at"`
	Finished     time.Time `json:"finished_at,omitempty"`
	OK           bool      `json:"ok"`
	Mode         string    `json:"mode,omitempty"`
	CommitSHA    string    `json:"commit_sha,omitempty"`
	Ref          string    `json:"ref,omitempty"`
	RolledBack   bool      `json:"rolled_back,omitempty"`
	RollbackSHA  string    `json:"rollback_sha,omitempty"`
	RollbackNote string    `json:"rollback_note,omitempty"`
	Error        string    `json:"error,omitempty"`
	LogTail      string    `json:"log_tail,omitempty"`
}

// Handler holds the deploy admin handlers and per-instance state.
type Handler struct {
	deps Deps

	// Per-app deploy locks for webhook serialization.
	deployLocks sync.Map // app name → *sync.Mutex

	// Last webhook deploy status per app.
	lastWebhookMu     sync.Mutex
	lastWebhookByName map[string]*WebhookDeployStatus

	// Deploy history per app (in-memory cache, persisted to disk).
	deployHistoryMu sync.Mutex
	deployHistory   map[string][]DeployHistoryEntry
}

// New creates a deploy Handler.
func New(deps Deps) *Handler {
	return &Handler{
		deps:              deps,
		lastWebhookByName: make(map[string]*WebhookDeployStatus),
		deployHistory:     make(map[string][]DeployHistoryEntry),
	}
}

// ── Helpers ──

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handler) getDeployLock(name string) *sync.Mutex {
	if m, ok := h.deployLocks.Load(name); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := h.deployLocks.LoadOrStore(name, m)
	return actual.(*sync.Mutex)
}

// ── Deploy handler ──

func (h *Handler) Deploy(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	appsMgr := h.deps.AppsManager()
	if appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	var req struct {
		GitURL    string            `json:"git_url"`
		Branch    string            `json:"branch"`
		BuildCmd  string            `json:"build_cmd"`
		Env       map[string]string `json:"env"`
		SSHKey    string            `json:"ssh_key_path"`
		GitToken  string            `json:"git_token"`
		SkipStart bool              `json:"skip_start"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Build a temporary config for validation.
	validationDef := *def
	if req.GitURL != "" {
		validationDef.Deploy.GitURL = req.GitURL
	}
	if req.Branch != "" {
		validationDef.Deploy.GitBranch = req.Branch
	}
	if req.BuildCmd != "" {
		validationDef.Deploy.BuildCmd = req.BuildCmd
	}
	if req.SSHKey != "" {
		validationDef.Deploy.SSHKeyPath = req.SSHKey
	}
	if req.GitToken != "" {
		validationDef.Deploy.GitToken = req.GitToken
	}
	if req.Env != nil {
		validationDef.Env = req.Env
	}
	if err := h.deps.ValidateDeployConfig(&validationDef); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateDockerGitDeploy(&validationDef); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Acquire per-app lock.
	lock := h.getDeployLock(name)
	lock.Lock()
	defer lock.Unlock()

	logBuf := &strings.Builder{}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	rollbackSHA := currentGitSHA(ctx, def.WorkDir)

	// Merge env.
	env := def.Env
	if req.Env != nil {
		env = req.Env
	}

	if err := runDeployCore(ctx, def, validationDef.Deploy.GitURL, validationDef.Deploy.GitBranch, validationDef.Deploy.BuildCmd, validationDef.Deploy.SSHKeyPath, validationDef.Deploy.GitToken, env, logBuf); err != nil {
		resp := &AppDeployResponse{OK: false, Error: err.Error(), Log: logBuf.String(), LogTail: tailString(logBuf.String(), 4096)}
		if rollbackSHA != "" {
			rb, rbSHA, rbNote := h.deps.AppRollback(ctx, name, def, rollbackSHA, validationDef.Deploy, cloneStringMap(env), !req.SkipStart, logBuf)
			resp.RolledBack = rb
			resp.Error += " (rollback: " + rbSHA + " " + rbNote + ")"
		}
		h.deps.RecordAudit(r, "app.deploy", name+" error: "+err.Error(), false)
		jsonResponse(w, resp)
		return
	}

	commitSHA := ""
	if sha, err := runOutput(ctx, def.WorkDir, "git", "rev-parse", "HEAD"); err == nil {
		commitSHA = strings.TrimSpace(sha)
	}

	if err := h.deps.AppCompleteDeploy(name, def, req.SkipStart); err != nil {
		resp := &AppDeployResponse{OK: false, CommitSHA: commitSHA, Error: err.Error(), Log: logBuf.String(), LogTail: tailString(logBuf.String(), 4096), RollbackSHA: rollbackSHA}
		if rollbackSHA != "" {
			rb, rbSHA, rbNote := h.deps.AppRollback(ctx, name, def, rollbackSHA, validationDef.Deploy, cloneStringMap(env), true, logBuf)
			resp.RolledBack = rb
			resp.Error += " (rollback: " + rbSHA + " " + rbNote + ")"
			resp.RollbackSHA = rbSHA
		}
		h.deps.RecordAudit(r, "app.deploy", name+" post-deploy error: "+err.Error(), false)
		jsonResponse(w, resp)
		return
	}

	h.deps.RecordAudit(r, "app.deploy", name+" @ "+commitSHA, true)
	h.recordHistory(name, DeployHistoryEntry{
		Source: "manual", StartedAt: time.Now(), Finished: time.Now(),
		OK: true, CommitSHA: commitSHA, LogTail: tailString(logBuf.String(), 2048),
	})
	if err := h.deps.Reload(); err != nil {
		h.deps.LogError("reload after deploy failed", "error", err)
	}
	jsonResponse(w, &AppDeployResponse{
		OK: true, CommitSHA: commitSHA, Message: "deployed successfully",
		Log: logBuf.String(), LogTail: tailString(logBuf.String(), 2048),
	})
}

func (h *Handler) DeployPreflight(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	appsMgr := h.deps.AppsManager()
	if appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	jsonResponse(w, appDeployPreflight(def))
}

// ── Webhook handler ──

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	appsMgr := h.deps.AppsManager()
	if appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if def.Deploy.WebhookSecret == "" {
		jsonError(w, "webhooks are not enabled for this app", http.StatusForbidden)
		return
	}
	if def.Deploy.GitURL == "" {
		jsonError(w, "no git source configured; run /deploy first", http.StatusConflict)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !verifyWebhookSignature(r, body, def.Deploy.WebhookSecret) {
		jsonError(w, "signature mismatch", http.StatusUnauthorized)
		return
	}
	ref := extractPushRef(body)
	branch := ""
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		branch = ref[i+1:]
	}
	wantBranch := def.Deploy.BranchFilter
	if wantBranch == "" {
		wantBranch = def.Deploy.GitBranch
	}
	if wantBranch != "" && branch != "" && branch != wantBranch {
		h.deps.RecordAudit(r, "app.webhook.skip",
			fmt.Sprintf("%s ref=%s want=%s", name, ref, wantBranch), true)
		w.WriteHeader(http.StatusAccepted)
		jsonResponse(w, map[string]any{
			"status": "skipped",
			"reason": fmt.Sprintf("push was on %q, app tracks %q", branch, wantBranch),
		})
		return
	}
	h.deps.RecordAudit(r, "app.webhook.accept",
		fmt.Sprintf("%s ref=%s", name, ref), true)
	go h.runWebhookDeploy(name, ref)
	w.WriteHeader(http.StatusAccepted)
	jsonResponse(w, map[string]any{
		"status": "accepted",
		"name":   name,
		"ref":    ref,
	})
}

func (h *Handler) WebhookStatus(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	h.lastWebhookMu.Lock()
	st := h.lastWebhookByName[name]
	h.lastWebhookMu.Unlock()
	if st == nil {
		jsonResponse(w, map[string]any{"name": name, "status": "no webhook deploys yet"})
		return
	}
	jsonResponse(w, st)
}

func (h *Handler) DeployHistory(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	h.deployHistoryMu.Lock()
	history := append([]DeployHistoryEntry(nil), h.deployHistory[name]...)
	h.deployHistoryMu.Unlock()
	if len(history) == 0 {
		appsMgr := h.deps.AppsManager()
		if appsMgr != nil {
			history = loadDeployHistory(appsMgr.Store().Dir, name)
			if len(history) > 0 {
				h.deployHistoryMu.Lock()
				h.deployHistory[name] = append([]DeployHistoryEntry(nil), history...)
				h.deployHistoryMu.Unlock()
			}
		}
	}
	jsonResponse(w, map[string]any{
		"name":  name,
		"items": history,
	})
}

// RunWebhookDeploy is exported for the admin adapter.
func (h *Handler) RunWebhookDeploy(name, ref string) { h.runWebhookDeploy(name, ref) }

func (h *Handler) recordHistory(name string, entry DeployHistoryEntry) {
	h.deployHistoryMu.Lock()
	items := append([]DeployHistoryEntry{entry}, h.deployHistory[name]...)
	if len(items) > 20 {
		items = items[:20]
	}
	h.deployHistory[name] = items
	h.deployHistoryMu.Unlock()
	appsMgr := h.deps.AppsManager()
	if appsMgr != nil {
		_ = persistDeployHistory(appsMgr.Store().Dir, name, items)
	}
}

func (h *Handler) recordLastWebhook(name string, status *WebhookDeployStatus) {
	h.lastWebhookMu.Lock()
	h.lastWebhookByName[name] = status
	h.lastWebhookMu.Unlock()
}

func (h *Handler) runWebhookDeploy(name, ref string) {
	defer func() {
		if rec := recover(); rec != nil {
			h.deps.LogError("webhook deploy panic", "app", name, "panic", rec)
		}
	}()

	lock := h.getDeployLock(name)
	lock.Lock()
	defer lock.Unlock()

	status := &WebhookDeployStatus{
		StartedAt: time.Now(),
		Ref:       ref,
	}

	appsMgr := h.deps.AppsManager()
	def, err := appsMgr.Store().Get(name)
	if err != nil || def == nil {
		status.OK = false
		status.Error = "app disappeared between webhook and deploy"
		status.Finished = time.Now()
		h.recordLastWebhook(name, status)
		h.recordHistory(name, DeployHistoryEntry{
			Source: "webhook", StartedAt: status.StartedAt, Finished: status.Finished,
			OK: false, Ref: ref, Error: status.Error,
		})
		return
	}

	if def.WorkDir == "" {
		status.OK = false
		status.Error = "app has no work_dir resolved"
		status.Finished = time.Now()
		h.recordLastWebhook(name, status)
		h.recordHistory(name, DeployHistoryEntry{
			Source: "webhook", StartedAt: status.StartedAt, Finished: status.Finished,
			OK: false, Ref: ref, Error: status.Error,
		})
		return
	}
	if err := validateDockerGitDeploy(def); err != nil {
		status.OK = false
		status.Error = err.Error()
		status.Finished = time.Now()
		h.recordLastWebhook(name, status)
		h.recordHistory(name, DeployHistoryEntry{
			Source: "webhook", StartedAt: status.StartedAt, Finished: status.Finished,
			OK: false, Ref: ref, Error: status.Error,
		})
		return
	}

	logBuf := &strings.Builder{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	rollbackSHA := currentGitSHA(ctx, def.WorkDir)

	if err := runDeployCore(ctx, def, def.Deploy.GitURL, def.Deploy.GitBranch, def.Deploy.BuildCmd, def.Deploy.SSHKeyPath, def.Deploy.GitToken, def.Env, logBuf); err != nil {
		status.OK = false
		status.Error = err.Error()
		if rollbackSHA != "" {
			status.RolledBack, status.RollbackSHA, status.RollbackNote = h.deps.AppRollback(ctx, name, def, rollbackSHA, def.Deploy, cloneStringMap(def.Env), false, logBuf)
		}
		status.LogTail = tailString(logBuf.String(), 4096)
		status.Finished = time.Now()
		h.recordLastWebhook(name, status)
		h.recordHistory(name, DeployHistoryEntry{
			Source: "webhook", StartedAt: status.StartedAt, Finished: status.Finished,
			OK: false, Ref: ref, Error: status.Error,
			RolledBack: status.RolledBack, RollbackSHA: status.RollbackSHA, RollbackNote: status.RollbackNote,
			LogTail: status.LogTail,
		})
		return
	}

	if sha, err := runOutput(ctx, def.WorkDir, "git", "rev-parse", "HEAD"); err == nil {
		status.CommitSHA = strings.TrimSpace(sha)
	}

	if err := h.deps.AppCompleteDeploy(name, def, false); err != nil {
		status.OK = false
		status.Error = err.Error()
		if rollbackSHA != "" {
			status.RolledBack, status.RollbackSHA, status.RollbackNote = h.deps.AppRollback(ctx, name, def, rollbackSHA, def.Deploy, cloneStringMap(def.Env), true, logBuf)
		}
		status.LogTail = tailString(logBuf.String(), 4096)
		status.Finished = time.Now()
		h.recordLastWebhook(name, status)
		h.recordHistory(name, DeployHistoryEntry{
			Source: "webhook", StartedAt: status.StartedAt, Finished: status.Finished,
			OK: false, CommitSHA: status.CommitSHA, Ref: ref, Error: status.Error,
			RolledBack: status.RolledBack, RollbackSHA: status.RollbackSHA, RollbackNote: status.RollbackNote,
			LogTail: status.LogTail,
		})
		return
	}

	status.OK = true
	status.LogTail = tailString(logBuf.String(), 2048)
	status.Finished = time.Now()
	h.recordLastWebhook(name, status)
	h.recordHistory(name, DeployHistoryEntry{
		Source: "webhook", StartedAt: status.StartedAt, Finished: status.Finished,
		OK: true, CommitSHA: status.CommitSHA, Ref: ref, LogTail: status.LogTail,
	})
	_ = h.deps.Reload()
	h.deps.LogInfo("webhook deploy ok",
		"app", name, "commit", status.CommitSHA, "duration", status.Finished.Sub(status.StartedAt))
}

func (h *Handler) GenerateDeployKey(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	appsMgr := h.deps.AppsManager()
	if appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	privatePath, publicKey, err := generateAppDeployKey(appsMgr.Store().Dir, name)
	if err != nil {
		h.deps.RecordAudit(r, "app.deploy_key", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	def.Deploy.SSHKeyPath = privatePath
	if err := h.deps.ValidateDeployConfig(def); err != nil {
		h.deps.RecordAudit(r, "app.deploy_key", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := appsMgr.Store().Save(def); err != nil {
		h.deps.RecordAudit(r, "app.deploy_key", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "app.deploy_key", name, true)
	jsonResponse(w, AppDeployKeyResponse{
		PrivateKeyPath: privatePath,
		PublicKey:      publicKey,
	})
}
