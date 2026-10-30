package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNotifyTestConfigErrorsAre400: POST /notify/test answered 500 for a
// channel the caller had configured wrongly (unknown type, missing field),
// which reads as a server fault and trips 5xx alerting.
func TestNotifyTestConfigErrorsAre400(t *testing.T) {
	for name, body := range map[string]string{
		"unknown type":     `{"type":"carrier-pigeon"}`,
		"slack no url":     `{"type":"slack","config":{}}`,
		"webhook no url":   `{"type":"webhook","config":{}}`,
		"email no host":    `{"type":"email","config":{"to":"a@b.c"}}`,
		"telegram no chat": `{"type":"telegram","config":{"bot_token":"x"}}`,
		"loopback webhook": `{"type":"webhook","config":{"url":"http://127.0.0.1:9/x"}}`,
	} {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleNotifyTest(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/notify/test", strings.NewReader(body))))
		if rec.Code != 400 {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}
