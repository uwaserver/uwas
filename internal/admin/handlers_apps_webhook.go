package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Webhook auto-deploy. Pushes from GitHub / GitLab hit
// POST /api/v1/apps/{name}/webhook with a provider-
// specific payload + signature header. We verify the HMAC against
// the app's stored WebhookSecret, parse the push event, and kick off
// the same git-clone + build + restart flow as manual /deploy.
//
// Public surface deliberately small:
//   - returns 202 immediately so GitHub doesn't retry on the 10-second
//     delivery timeout
//   - runs the deploy in a background goroutine with its own
//     30-minute context (webhooks can come in for large builds)
//   - serializes per-app via a deploy lock so two simultaneous pushes
//     don't interleave their git operations on the same workdir

// deployLocks holds a sync.Mutex per app name so webhook-triggered
// deploys for the SAME app run serially. Concurrent deploys to
// DIFFERENT apps still parallelize freely.
type deployLockMap struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var deployLocks = &deployLockMap{locks: make(map[string]*sync.Mutex)}

func (d *deployLockMap) get(name string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m, ok := d.locks[name]; ok {
		return m
	}
	m := &sync.Mutex{}
	d.locks[name] = m
	return m
}

// lastDeployStatus tracks the most recent webhook deploy outcome per
// app so a follow-up `GET /webhook-status` returns something useful.
// Bounded to the latest entry — ops can scroll runtime logs for older
// history.
type webhookDeployStatus struct {
	StartedAt time.Time `json:"started_at"`
	Finished  time.Time `json:"finished_at,omitempty"`
	OK        bool      `json:"ok"`
	CommitSHA string    `json:"commit_sha,omitempty"`
	Ref       string    `json:"ref,omitempty"`
	Error     string    `json:"error,omitempty"`
	LogTail   string    `json:"log_tail,omitempty"`
}

var (
	lastWebhookMu     sync.Mutex
	lastWebhookByName = make(map[string]*webhookDeployStatus)
)

// handleAppWebhook is the public push-event receiver.
// Auth is HMAC-only — no admin cookie required, because GitHub/
// GitLab webhook deliveries don't carry one. Anyone with the secret
// can trigger a deploy, which is the point.
func (s *Server) handleAppWebhook(w http.ResponseWriter, r *http.Request) {
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
		// Be deliberately vague — don't help attackers enumerate
		// configured apps via webhook 404 vs 200.
		jsonError(w, "not found", http.StatusNotFound)
		return
	}

	if def.Deploy.WebhookSecret == "" {
		jsonError(w, "webhooks are not enabled for this app", http.StatusForbidden)
		return
	}
	if def.Deploy.GitURL == "" {
		// A webhook can't deploy if there's no source — operator
		// must run /deploy manually at least once to seed git_url.
		jsonError(w, "no git source configured; run /deploy first", http.StatusConflict)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Signature verification. GitHub signs with X-Hub-Signature-256
	// (HMAC-SHA256 over the raw body, hex-encoded). GitLab uses
	// X-Gitlab-Token (the token verbatim, no HMAC). We support both.
	if !verifyWebhookSignature(r, body, def.Deploy.WebhookSecret) {
		jsonError(w, "signature mismatch", http.StatusUnauthorized)
		return
	}

	// Parse just enough of the payload to extract the ref. Errors
	// are non-fatal — if we can't parse, we fall back to "deploy
	// whatever the default branch is" rather than rejecting.
	ref := extractPushRef(body)
	branch := ""
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		branch = ref[i+1:]
	}

	// Branch filter — only deploy on pushes to the configured branch.
	wantBranch := def.Deploy.BranchFilter
	if wantBranch == "" {
		wantBranch = def.Deploy.GitBranch
	}
	if wantBranch != "" && branch != "" && branch != wantBranch {
		// Acknowledge but don't deploy — GitHub will mark this as a
		// successful delivery, which is the right outcome.
		s.recordAuditR(r, "app.webhook.skip",
			fmt.Sprintf("%s ref=%s want=%s", name, ref, wantBranch), true)
		w.WriteHeader(http.StatusAccepted)
		jsonResponse(w, map[string]any{
			"status": "skipped",
			"reason": fmt.Sprintf("push was on %q, app tracks %q", branch, wantBranch),
		})
		return
	}

	s.recordAuditR(r, "app.webhook.accept",
		fmt.Sprintf("%s ref=%s", name, ref), true)

	// Kick the deploy off async — GitHub treats >10s response time
	// as a delivery failure and retries, which would clobber our
	// per-app deploy lock with a queue of redundant runs.
	go s.runWebhookDeploy(name, ref)

	w.WriteHeader(http.StatusAccepted)
	jsonResponse(w, map[string]any{
		"status": "accepted",
		"name":   name,
		"ref":    ref,
	})
}

// handleAppWebhookStatus reports the outcome of the most
// recent webhook-triggered deploy for an app. Used by the dashboard
// to show "last auto-deploy: 5 min ago, ok" without polling logs.
func (s *Server) handleAppWebhookStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	lastWebhookMu.Lock()
	st := lastWebhookByName[name]
	lastWebhookMu.Unlock()
	if st == nil {
		jsonResponse(w, map[string]any{"name": name, "status": "no webhook deploys yet"})
		return
	}
	jsonResponse(w, st)
}

// runWebhookDeploy is the goroutine entrypoint for webhook-triggered
// deploys. Serializes per-app via deployLocks, runs the same flow as
// the manual /deploy handler, then records the outcome in
// lastWebhookByName.
func (s *Server) runWebhookDeploy(name, ref string) {
	defer func() {
		if rec := recover(); rec != nil && s.logger != nil {
			s.logger.Error("webhook deploy panic", "app", name, "panic", rec)
		}
	}()

	lock := deployLocks.get(name)
	lock.Lock()
	defer lock.Unlock()

	status := &webhookDeployStatus{
		StartedAt: time.Now(),
		Ref:       ref,
	}

	def, err := s.appsMgr.Store().Get(name)
	if err != nil || def == nil {
		status.OK = false
		status.Error = "app disappeared between webhook and deploy"
		status.Finished = time.Now()
		s.recordLastWebhook(name, status)
		return
	}

	if def.WorkDir == "" {
		status.OK = false
		status.Error = "app has no work_dir resolved"
		status.Finished = time.Now()
		s.recordLastWebhook(name, status)
		return
	}
	if err := validateDockerGitDeploy(def); err != nil {
		status.OK = false
		status.Error = err.Error()
		status.Finished = time.Now()
		s.recordLastWebhook(name, status)
		return
	}

	logBuf := &strings.Builder{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := runDeployCore(ctx, def, def.Deploy.GitURL, def.Deploy.GitBranch, def.Deploy.BuildCmd, def.Env, logBuf); err != nil {
		status.OK = false
		status.Error = err.Error()
		status.LogTail = tailString(logBuf.String(), 4096)
		status.Finished = time.Now()
		s.recordLastWebhook(name, status)
		if s.logger != nil {
			s.logger.Warn("webhook deploy failed", "app", name, "error", err)
		}
		return
	}

	// Capture commit SHA.
	if sha, err := runOutput(ctx, def.WorkDir, "git", "rev-parse", "HEAD"); err == nil {
		status.CommitSHA = strings.TrimSpace(sha)
	}

	// Restart + verify listening.
	if err := s.appsMgr.Restart(name); err != nil {
		status.OK = false
		status.Error = "deploy succeeded but restart failed: " + err.Error()
		status.LogTail = tailString(logBuf.String(), 4096)
		status.Finished = time.Now()
		s.recordLastWebhook(name, status)
		return
	}
	if err := s.appsMgr.WaitListening(name, listeningProbeTimeout); err != nil {
		status.OK = false
		status.Error = "deploy + restart ok but app is not listening: " + err.Error()
		status.LogTail = tailString(logBuf.String(), 4096)
		status.Finished = time.Now()
		s.recordLastWebhook(name, status)
		return
	}

	status.OK = true
	status.LogTail = tailString(logBuf.String(), 2048)
	status.Finished = time.Now()
	s.recordLastWebhook(name, status)

	s.maybeReloadForApps()
	if s.logger != nil {
		s.logger.Info("webhook deploy ok",
			"app", name, "commit", status.CommitSHA, "duration", status.Finished.Sub(status.StartedAt))
	}
}

// recordLastWebhook stashes the outcome of the most recent webhook
// deploy for an app so the status endpoint can return it.
func (s *Server) recordLastWebhook(name string, status *webhookDeployStatus) {
	lastWebhookMu.Lock()
	lastWebhookByName[name] = status
	lastWebhookMu.Unlock()
}

// verifyWebhookSignature validates either GitHub-style HMAC-SHA256
// (X-Hub-Signature-256: "sha256=HEX") or GitLab-style shared-token
// (X-Gitlab-Token: <secret>). Returns true on success. Constant-time
// comparison throughout to defeat timing oracles.
func verifyWebhookSignature(r *http.Request, body []byte, secret string) bool {
	// GitHub
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
	// GitLab
	if token := r.Header.Get("X-Gitlab-Token"); token != "" {
		return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
	}
	// Bitbucket and others can be added here; fall through to fail.
	return false
}

// extractPushRef pulls the "refs/heads/branch" string out of a
// GitHub or GitLab push payload. Returns "" if the body isn't valid
// JSON or doesn't contain a ref — caller falls back to "deploy
// default branch".
func extractPushRef(body []byte) string {
	var payload struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Ref
}

// tailString returns the last n bytes of s, stripped of surrounding
// whitespace. Used to bound the size of LogTail in webhook status
// snapshots — full logs go to the runtime log file.
func tailString(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[len(s)-n:])
}
