package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// signGitHub mirrors what GitHub does when POSTing a webhook: HMAC-
// SHA256 over the raw body, hex-encoded, prefixed with "sha256=".
func signGitHub(body, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

func TestVerifyWebhookSignatureGitHubValid(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "super-secret"
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", signGitHub(string(body), secret))
	if !verifyWebhookSignature(req, body, secret) {
		t.Error("valid GitHub signature should verify")
	}
}

func TestVerifyWebhookSignatureGitHubInvalid(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	if verifyWebhookSignature(req, body, "super-secret") {
		t.Error("forged signature should not verify")
	}
}

func TestVerifyWebhookSignatureGitHubWrongSecret(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", signGitHub(string(body), "wrong"))
	if verifyWebhookSignature(req, body, "right") {
		t.Error("signature with wrong secret should not verify")
	}
}

func TestVerifyWebhookSignatureGitLabValid(t *testing.T) {
	body := []byte(`{}`)
	req := httptest.NewRequest("POST", "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "super-secret")
	if !verifyWebhookSignature(req, body, "super-secret") {
		t.Error("valid GitLab token should verify")
	}
}

func TestVerifyWebhookSignatureGitLabInvalid(t *testing.T) {
	body := []byte(`{}`)
	req := httptest.NewRequest("POST", "/webhook", nil)
	req.Header.Set("X-Gitlab-Token", "wrong-secret")
	if verifyWebhookSignature(req, body, "super-secret") {
		t.Error("wrong GitLab token should not verify")
	}
}

func TestVerifyWebhookSignatureNoHeaders(t *testing.T) {
	body := []byte(`{}`)
	req := httptest.NewRequest("POST", "/webhook", nil)
	if verifyWebhookSignature(req, body, "super-secret") {
		t.Error("webhook without signature header should not verify")
	}
}

func TestExtractPushRef(t *testing.T) {
	cases := map[string]string{
		`{"ref":"refs/heads/main"}`:        "refs/heads/main",
		`{"ref":"refs/heads/feature/x"}`:   "refs/heads/feature/x",
		`{"ref":"refs/tags/v1.0"}`:         "refs/tags/v1.0",
		`{}`:                               "",
		`{"other":"field"}`:                "",
		``:                                 "",
		`not json at all`:                  "",
	}
	for body, want := range cases {
		got := extractPushRef([]byte(body))
		if got != want {
			t.Errorf("extractPushRef(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestTailString(t *testing.T) {
	if got := tailString("hello world", 100); got != "hello world" {
		t.Errorf("short string should pass through, got %q", got)
	}
	if got := tailString("aaaaaaaaaaaaaaaa", 5); got != "aaaaa" {
		t.Errorf("long string should be truncated to last n, got %q", got)
	}
	if got := tailString("", 5); got != "" {
		t.Errorf("empty in should return empty, got %q", got)
	}
}

func TestDeployLocksAreSticky(t *testing.T) {
	// Same name → same mutex; different names → different mutexes.
	a := deployLocks.get("alice")
	b := deployLocks.get("alice")
	c := deployLocks.get("bob")
	if a != b {
		t.Error("deployLocks.get should return same mutex for same name")
	}
	if a == c {
		t.Error("deployLocks.get should return different mutex for different name")
	}
}

// Sanity test: ensure imports stay aligned even when only test code
// touches certain symbols.
var _ = http.StatusOK
