package settings

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// otherWriterPersistDeps fails PersistConfig after running onPersist once, as a
// concurrent request that changed the config while the failing save ran.
type otherWriterPersistDeps struct {
	stubDeps
	onPersist func(d *otherWriterPersistDeps)
}

func (d *otherWriterPersistDeps) PersistConfig() error {
	if f := d.onPersist; f != nil {
		d.onPersist = nil
		f(d)
	}
	return errors.New("disk full")
}

// F65500: a failed NotifyPrefsPut restored Alerting/Webhooks unconditionally,
// wiping a change another request (webhook create, SettingsPut) made while
// the failing save ran; that request's own save then dropped it from disk.
func TestNotifyPrefsRollbackKeepsOtherWritersChange(t *testing.T) {
	put := func(d *otherWriterPersistDeps, body string) int {
		rec := httptest.NewRecorder()
		New(d).NotifyPrefsPut(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
		return rec.Code
	}
	t.Run("webhooks", func(t *testing.T) {
		d := &otherWriterPersistDeps{}
		d.cfg.Global.Webhooks = []config.WebhookConfig{{URL: "https://w0.example/h", Enabled: true}}
		d.onPersist = func(d *otherWriterPersistDeps) {
			d.mu.Lock()
			d.cfg.Global.Webhooks = append(d.cfg.Global.Webhooks, config.WebhookConfig{URL: "https://x.example/h", Enabled: true})
			d.mu.Unlock()
		}
		if c := put(d, `{"webhooks":[{"url":"https://w1.example/h","enabled":true}]}`); c != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", c)
		}
		ws := d.cfg.Global.Webhooks
		if len(ws) != 2 || ws[1].URL != "https://x.example/h" {
			t.Fatalf("concurrently created webhook lost: %+v", ws)
		}
	})
	t.Run("alerting", func(t *testing.T) {
		d := &otherWriterPersistDeps{}
		d.onPersist = func(d *otherWriterPersistDeps) {
			d.mu.Lock()
			d.cfg.Global.Alerting.Enabled = true
			d.mu.Unlock()
		}
		put(d, `{"alerting":{"enabled":false,"webhook_url":"https://a.example/x"}}`)
		if !d.cfg.Global.Alerting.Enabled {
			t.Fatal("concurrent alerting change undone")
		}
	})
	t.Run("control: no other writer rolls back", func(t *testing.T) {
		d := &otherWriterPersistDeps{}
		d.cfg.Global.Webhooks = []config.WebhookConfig{{URL: "https://w0.example/h", Enabled: true}}
		put(d, `{"webhooks":[{"url":"https://w1.example/h","enabled":true}]}`)
		if ws := d.cfg.Global.Webhooks; len(ws) != 1 || ws[0].URL != "https://w0.example/h" {
			t.Fatalf("rejected change stayed live: %+v", ws)
		}
	})
}
