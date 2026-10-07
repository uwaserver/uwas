package deploy

// Regression guard: the app deploy webhook's branch filter must fail CLOSED.
//
// branch_filter / git_branch exist to stop a push on an unrelated branch from
// deploying a production app. The guard previously also required the parsed
// branch to be non-empty, so any event whose `ref` could not be read — a
// GitHub `ping`, a payload shape the parser does not recognise, or any
// non-push event that reached this endpoint — left branch empty, made the
// whole condition false, and ran a full deploy.
//
// The endpoint is deliberately unauthenticated (internal/admin/authmw
// exempts POST /api/v1/apps/{name}/webhook from the admin auth chain), so this
// is the only gate between a webhook holder and a production deploy.
//
// Contract, internal/apps/app.go: "BranchFilter, when set, makes the webhook
// ONLY trigger a deploy if the push event's ref ends in this branch name."

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

type branchFilterDeps struct {
	mgr     *apps.Manager
	audited []string
}

func (d *branchFilterDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool { return true }
func (d *branchFilterDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool   { return true }
func (d *branchFilterDeps) LogInfo(string, ...any)                                   {}
func (d *branchFilterDeps) LogWarn(string, ...any)                                   {}
func (d *branchFilterDeps) LogError(string, ...any)                                  {}
func (d *branchFilterDeps) RecordAudit(_ *http.Request, action, _ string, _ bool) {
	d.audited = append(d.audited, action)
}
func (d *branchFilterDeps) AppsManager() *apps.Manager                      { return d.mgr }
func (d *branchFilterDeps) ConfigPath() string                              { return "" }
func (d *branchFilterDeps) ValidateDeployConfig(*apps.App) error            { return nil }
func (d *branchFilterDeps) Reload() error                                   { return nil }
func (d *branchFilterDeps) AppCompleteDeploy(string, *apps.App, bool) error { return nil }
func (d *branchFilterDeps) AppRollback(context.Context, string, *apps.App, string, apps.DeployConfig, map[string]string, bool, LogSink) (bool, string, string) {
	return false, "", ""
}

func branchFilterHandler(t *testing.T, secret, branchFilter string) (*Handler, *branchFilterDeps) {
	t.Helper()
	store := apps.NewStore(t.TempDir())
	if err := store.Save(&apps.App{
		Name:    "demo",
		Runtime: apps.RuntimeNode,
		Command: "true",
		Deploy: apps.DeployConfig{
			GitURL:        "https://github.com/u/r.git",
			GitBranch:     "main",
			BranchFilter:  branchFilter,
			WebhookSecret: secret,
		},
	}); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	d := &branchFilterDeps{mgr: apps.NewManager(store, nil)}
	return New(d), d
}

func signedPush(secret, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/apps/demo/webhook", strings.NewReader(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("name", "demo")
	return r
}

func webhookStatus(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	s, _ := m["status"].(string)
	return s
}

// TestWebhookBranchFilterFailsClosedOnUnreadableRef is the regression: an
// event whose ref cannot be read must not satisfy branch_filter.
func TestWebhookBranchFilterFailsClosedOnUnreadableRef(t *testing.T) {
	const secret = "whsec_regression_secret"

	cases := []struct {
		name string
		body string
		why  string
	}{
		{"ping event has no ref", `{"zen":"Design for failure.","hook_id":1,"hook":{"type":"Repository"}}`, "a GitHub ping carries no ref"},
		{"ref is empty", `{"ref":"","after":"abc123","commits":[]}`, "ref is the empty string"},
		{"ref key missing", `{"repository":{"full_name":"u/r"},"pusher":{"name":"someone"}}`, "the payload has no ref key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, d := branchFilterHandler(t, secret, "main")
			rec := httptest.NewRecorder()
			h.Webhook(rec, signedPush(secret, tc.body))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202 (signature is valid, so the request is accepted then filtered); body=%s",
					rec.Code, rec.Body.String())
			}
			if got := webhookStatus(t, rec); got != "skipped" {
				t.Errorf("branch_filter=\"main\" was set but %s was accepted (status=%q). "+
					"An event the filter cannot vouch for must never satisfy it. body=%s",
					tc.why, got, rec.Body.String())
			}
			for _, a := range d.audited {
				if a == "app.webhook.accept" {
					t.Errorf("app.webhook.accept audited for a push whose ref could not be read; want app.webhook.skip")
				}
			}
		})
	}
}

// TestWebhookBranchFilterAcceptsMatchingBranch: the tracked branch still deploys.
func TestWebhookBranchFilterAcceptsMatchingBranch(t *testing.T) {
	const secret = "whsec_regression_secret"
	h, _ := branchFilterHandler(t, secret, "main")
	rec := httptest.NewRecorder()

	h.Webhook(rec, signedPush(secret, `{"ref":"refs/heads/main","after":"deadbeef"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if got := webhookStatus(t, rec); got != "accepted" {
		t.Errorf("push to the tracked branch must be accepted, got %q; body=%s", got, rec.Body.String())
	}
}

// TestWebhookBranchFilterSkipsOtherBranch: a readable ref on the wrong branch
// is still skipped — the case the original guard already handled.
func TestWebhookBranchFilterSkipsOtherBranch(t *testing.T) {
	const secret = "whsec_regression_secret"
	h, _ := branchFilterHandler(t, secret, "main")
	rec := httptest.NewRecorder()

	h.Webhook(rec, signedPush(secret, `{"ref":"refs/heads/staging","after":"cafebabe"}`))

	if got := webhookStatus(t, rec); got != "skipped" {
		t.Errorf("push to a non-tracked branch must be skipped, got %q; body=%s", got, rec.Body.String())
	}
}

// TestWebhookNoFilterStillAcceptsBlankRef: with no branch_filter and no
// git_branch configured, every push deploys ("leave both blank and any push
// triggers a deploy"). The fix must not turn that into a blanket refusal.
func TestWebhookNoFilterStillAcceptsBlankRef(t *testing.T) {
	const secret = "whsec_regression_secret"
	h, _ := branchFilterHandler(t, secret, "")
	// Clear git_branch too: wantBranch is then "" for every event.
	def, err := h.deps.AppsManager().Store().Get("demo")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	def.Deploy.GitBranch = ""
	if err := h.deps.AppsManager().Store().Save(def); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	rec := httptest.NewRecorder()
	h.Webhook(rec, signedPush(secret, `{"zen":"ping"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if got := webhookStatus(t, rec); got != "accepted" {
		t.Errorf("with no branch filter configured, any push must deploy, got %q; body=%s", got, rec.Body.String())
	}
}

// TestWebhookStillRejectsForgedSignature: the fix must not weaken auth.
func TestWebhookStillRejectsForgedSignature(t *testing.T) {
	const secret = "whsec_regression_secret"
	h, _ := branchFilterHandler(t, secret, "main")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/apps/demo/webhook",
		strings.NewReader(`{"ref":"refs/heads/main"}`))
	r.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("name", "demo")
	rec := httptest.NewRecorder()

	h.Webhook(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("forged signature must be refused with 401, got %d; body=%s", rec.Code, rec.Body.String())
	}
}
