package deploy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestWebhookPayloadJSONFormEncoded is the regression for the 415 / empty-ref
// pair. GitHub's default webhook content type is form-encoded, and the JSON
// arrives in a "payload" field rather than as the body itself.
func TestWebhookPayloadJSONFormEncoded(t *testing.T) {
	payload := `{"ref":"refs/heads/main"}`
	body := []byte("payload=" + url.QueryEscape(payload))

	r, _ := http.NewRequest(http.MethodPost, "/api/v1/apps/x/webhook", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if got := string(webhookPayloadJSON(r, body)); got != payload {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if ref := extractPushRef(webhookPayloadJSON(r, body)); ref != "refs/heads/main" {
		t.Errorf("ref = %q, want refs/heads/main", ref)
	}
}

func TestWebhookPayloadJSONVariants(t *testing.T) {
	jsonBody := []byte(`{"ref":"refs/heads/dev"}`)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		wantRef     string
	}{
		{"application/json", "application/json", jsonBody, "refs/heads/dev"},
		{"json with charset", "application/json; charset=utf-8", jsonBody, "refs/heads/dev"},
		{"form with charset", "application/x-www-form-urlencoded; charset=utf-8",
			[]byte("payload=" + url.QueryEscape(`{"ref":"refs/heads/dev"}`)), "refs/heads/dev"},
		// GitLab posts JSON with no content type games; an absent header must
		// not be treated as a form.
		{"no content type", "", jsonBody, "refs/heads/dev"},
		// A form body with no payload field falls back to the raw bytes rather
		// than inventing one.
		{"form without payload field", "application/x-www-form-urlencoded", jsonBody, "refs/heads/dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "/api/v1/apps/x/webhook", strings.NewReader(""))
			if tt.contentType != "" {
				r.Header.Set("Content-Type", tt.contentType)
			}
			if ref := extractPushRef(webhookPayloadJSON(r, tt.body)); ref != tt.wantRef {
				t.Errorf("ref = %q, want %q", ref, tt.wantRef)
			}
		})
	}
}

// TestWebhookSignatureUsesRawBody pins the ordering that matters: GitHub HMACs
// the bytes it put on the wire, so the signature must be checked against the
// form body, not against the JSON extracted from it.
func TestWebhookSignatureUsesRawBody(t *testing.T) {
	const secret = "s3cret"
	payload := `{"ref":"refs/heads/main"}`
	raw := []byte("payload=" + url.QueryEscape(payload))

	r, _ := http.NewRequest(http.MethodPost, "/api/v1/apps/x/webhook", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Hub-Signature-256", "sha256="+hmacHex(secret, raw))

	if !verifyWebhookSignature(r, raw, secret) {
		t.Error("signature over the raw form body was rejected")
	}
	if verifyWebhookSignature(r, webhookPayloadJSON(r, raw), secret) {
		t.Error("signature verified against the extracted JSON — it must use the raw body")
	}
}

func hmacHex(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
