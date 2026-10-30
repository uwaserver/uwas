package deploy

// F23800: an app that tracks no branch (git_branch and branch_filter empty =
// repo default) used to deploy on EVERY signed webhook delivery. GitHub sends a
// `ping` when the hook is created, and "Send me everything" subscribes it to
// issues, stars, pull requests and more; each of those ran a full fetch, build
// and restart of the production app. Only push events may deploy. Deliveries
// that carry no event header (other providers, curl) keep their old behaviour.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
)

func untrackedBranchHandler(t *testing.T, secret string) (*Handler, *branchFilterDeps) {
	t.Helper()
	store := apps.NewStore(t.TempDir())
	store.DataRoot = t.TempDir()
	workDir := store.DefaultWorkDir("demo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&apps.App{
		Name: "demo", Runtime: apps.RuntimeNode, Command: "true",
		Deploy: apps.DeployConfig{GitURL: "https://github.com/u/r.git", WebhookSecret: secret},
	}); err != nil {
		t.Fatal(err)
	}
	d := &branchFilterDeps{mgr: apps.NewManager(store, nil)}
	h := New(d)
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			h.lastWebhookMu.Lock()
			st := h.lastWebhookByName["demo"]
			h.lastWebhookMu.Unlock()
			if st != nil && !st.Finished.IsZero() {
				return
			}
			accepted := false
			for _, a := range d.auditedSnapshot() {
				accepted = accepted || a == "app.webhook.accept"
			}
			if !accepted {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	return h, d
}

func TestWebhookNonPushEventDoesNotDeploy(t *testing.T) {
	const secret = "whsec_event_type"
	cases := []struct {
		name, header, value, body string
		want                      string
	}{
		{"github ping", "X-GitHub-Event", "ping", `{"zen":"Keep it logically awesome."}`, "skipped"},
		{"github star", "X-GitHub-Event", "star", `{"action":"created"}`, "skipped"},
		{"gitlab merge request", "X-Gitlab-Event", "Merge Request Hook", `{"object_kind":"merge_request"}`, "skipped"},
		{"github push (control)", "X-GitHub-Event", "push", `{"ref":"refs/heads/main"}`, "accepted"},
		{"gitlab push (control)", "X-Gitlab-Event", "Push Hook", `{"ref":"refs/heads/main"}`, "accepted"},
		{"no event header (control)", "", "", `{"ref":"refs/heads/main"}`, "accepted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, d := untrackedBranchHandler(t, secret)
			req := signedPush(secret, c.body)
			if c.header != "" {
				req.Header.Set(c.header, c.value)
			}
			rec := httptest.NewRecorder()
			h.Webhook(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
			got := webhookStatus(t, rec)
			if got == "accepted" {
				// The accepted delivery deploys in the background; let it finish
				// before the temp dir is removed. Registered after the helper's
				// cleanup, so it runs first.
				t.Cleanup(func() {
					deadline := time.Now().Add(10 * time.Second)
					for time.Now().Before(deadline) {
						h.lastWebhookMu.Lock()
						st := h.lastWebhookByName["demo"]
						h.lastWebhookMu.Unlock()
						if st != nil && !st.Finished.IsZero() {
							return
						}
						time.Sleep(5 * time.Millisecond)
					}
				})
			}
			t.Logf("EXPECTED: %s\nACTUAL: %s", c.want, got)
			if got != c.want {
				t.Fatalf("PROBLEM CONFIRMED: %s delivery answered %q, want %q (audit: %v)", c.value, got, c.want, d.auditedSnapshot())
			}
		})
	}
}
