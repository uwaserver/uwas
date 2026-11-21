package settings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F65501: a masked webhook secret was restored from the n-th live webhook with
// the same URL, so dropping or reordering one of two such webhooks gave the
// kept entry its neighbour's secret. The mask's last four characters now pick
// the webhook it came from.
func TestNotifyPrefsMaskedSecretFollowsItsWebhook(t *testing.T) {
	const u = "https://hook.example/in"
	live := func() *stubDeps {
		d := &stubDeps{}
		d.cfg.Global.Webhooks = []config.WebhookConfig{
			{URL: u, Secret: "secret-one-AAAA", Enabled: true},
			{URL: u, Secret: "secret-two-BBBB", Enabled: true},
		}
		return d
	}
	put := func(d *stubDeps, body string) {
		rec := httptest.NewRecorder()
		New(d).NotifyPrefsPut(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
	}
	e := func(mask string) string { return `{"url":"` + u + `","secret":"` + mask + `","enabled":true}` }

	d := live()
	put(d, `{"webhooks":[`+e("****BBBB")+`]}`)
	if got := d.cfg.Global.Webhooks[0].Secret; got != "secret-two-BBBB" {
		t.Errorf("drop first: kept entry secret = %q, want secret-two-BBBB", got)
	}

	d = live()
	put(d, `{"webhooks":[`+e("****BBBB")+`,`+e("****AAAA")+`]}`)
	if ws := d.cfg.Global.Webhooks; ws[0].Secret != "secret-two-BBBB" || ws[1].Secret != "secret-one-AAAA" {
		t.Errorf("swap: secrets = %q, %q", ws[0].Secret, ws[1].Secret)
	}

	d = live()
	put(d, `{"webhooks":[`+e("****AAAA")+`,`+e("****BBBB")+`]}`)
	if ws := d.cfg.Global.Webhooks; ws[0].Secret != "secret-one-AAAA" || ws[1].Secret != "secret-two-BBBB" {
		t.Errorf("round trip: secrets = %q, %q", ws[0].Secret, ws[1].Secret)
	}
}
