package settings

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F65300: PUT /settings/notifications refuses the webhook URLs and headers the
// create endpoint refuses, instead of saving a webhook no delivery can send.
func TestNotifyPrefsRejectsUndeliverableWebhooks(t *testing.T) {
	for name, body := range map[string]string{
		"CRLF header value": `{"webhooks":[{"url":"https://h.example/x","events":["domain.add"],"headers":{"X-Token":"a\r\nX-Injected: 1"}}]}`,
		"CRLF header key":   `{"webhooks":[{"url":"https://h.example/x","events":["domain.add"],"headers":{"X-A\r\nX-B":"v"}}]}`,
		"non-http scheme":   `{"webhooks":[{"url":"ftp://h.example/x","events":["domain.add"]}]}`,
		"empty URL":         `{"webhooks":[{"url":"","events":["domain.add"]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			d := &stubDeps{}
			d.cfg.Global.Webhooks = []config.WebhookConfig{{URL: "https://old.example/e", Events: []string{"domain.add"}}}
			before := append([]config.WebhookConfig(nil), d.cfg.Global.Webhooks...)
			rec := httptest.NewRecorder()
			New(d).NotifyPrefsPut(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !reflect.DeepEqual(d.cfg.Global.Webhooks, before) {
				t.Fatalf("webhooks changed by a refused update: %#v", d.cfg.Global.Webhooks)
			}
		})
	}
	t.Run("control: valid entry saved", func(t *testing.T) {
		d := &stubDeps{}
		rec := httptest.NewRecorder()
		New(d).NotifyPrefsPut(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"webhooks":[{"url":"http://h.example/x","events":["domain.add"],"headers":{"X-Token":"abc"}}]}`)))
		if rec.Code != http.StatusOK || len(d.cfg.Global.Webhooks) != 1 {
			t.Fatalf("status = %d stored = %d, want 200/1", rec.Code, len(d.cfg.Global.Webhooks))
		}
	})
}
