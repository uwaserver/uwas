package deploy

// F23801: the webhook's 202 answers (accepted / skipped) set Content-Type
// AFTER WriteHeader, which net/http ignores; the JSON body was then served as
// sniffed text/plain. Providers that show the delivery response, and any
// client that dispatches on Content-Type, saw a non-JSON answer.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookAcceptedAndSkippedAnswerAsJSON(t *testing.T) {
	const secret = "whsec_content_type"
	cases := []struct {
		name, body, filter string
		badSig             bool
	}{
		{"accepted", `{"ref":"refs/heads/main"}`, "", false},
		{"skipped by branch filter", `{"ref":"refs/heads/dev"}`, "main", false},
		{"rejected signature (control)", `{"ref":"refs/heads/main"}`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := branchFilterHandler(t, secret, c.filter)
			rec := httptest.NewRecorder()
			signer := secret
			if c.badSig {
				signer = "not-the-secret"
			}
			h.Webhook(rec, signedPush(signer, c.body))
			ct := rec.Result().Header.Get("Content-Type")
			t.Logf("EXPECTED: application/json\nACTUAL: %q", ct)
			if !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("PROBLEM CONFIRMED: Content-Type = %q", ct)
			}
		})
	}
}
