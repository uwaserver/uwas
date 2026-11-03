package settings

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestNotifyPrefsRolledBackWhenPersistFails(t *testing.T) {
	d := &failPersistDeps{}
	d.cfg.Global.Alerting.SlackURL = "https://old.example/hook"
	d.cfg.Global.Webhooks = []config.WebhookConfig{{URL: "https://old.example/event", Events: []string{"domain.add"}}}
	wantAlerting := d.cfg.Global.Alerting
	wantWebhooks := append([]config.WebhookConfig(nil), d.cfg.Global.Webhooks...)

	body := `{"alerting":{"slack_url":"https://new.example/hook"},"webhooks":[{"url":"https://new.example/event","events":["domain.delete"]}]}`
	rec := httptest.NewRecorder()
	New(d).NotifyPrefsPut(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !reflect.DeepEqual(d.cfg.Global.Alerting, wantAlerting) {
		t.Fatalf("alerting changed after failed persist: %#v", d.cfg.Global.Alerting)
	}
	if !reflect.DeepEqual(d.cfg.Global.Webhooks, wantWebhooks) {
		t.Fatalf("webhooks changed after failed persist: %#v", d.cfg.Global.Webhooks)
	}
}
