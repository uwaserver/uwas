package settings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F65901: masked webhook header values were restored from the n-th live
// webhook with the same URL while the masked secret followed the webhook its
// mask came from (F65501), so dropping or reordering one of two such webhooks
// gave the kept entry its own secret but its neighbour's headers.
func TestNotifyPrefsMaskedHeadersFollowTheirWebhook(t *testing.T) {
	const u = "https://hook.example/in"
	live := func() *stubDeps {
		d := &stubDeps{}
		d.cfg.Global.Webhooks = []config.WebhookConfig{
			{URL: u, Secret: "secret-one-AAAA", Headers: map[string]string{"Authorization": "token-A"}, Enabled: true},
			{URL: u, Secret: "secret-two-BBBB", Headers: map[string]string{"Authorization": "token-B"}, Enabled: true},
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
	e := func(mask string) string {
		return `{"url":"` + u + `","secret":"` + mask + `","headers":{"Authorization":"********"},"enabled":true}`
	}
	pairs := func(d *stubDeps) string {
		var out []string
		for _, w := range d.cfg.Global.Webhooks {
			out = append(out, w.Secret+"/"+w.Headers["Authorization"])
		}
		return strings.Join(out, " ")
	}

	for _, tc := range []struct{ name, body, want string }{
		{"drop first", `{"webhooks":[` + e("****BBBB") + `]}`, "secret-two-BBBB/token-B"},
		{"swap", `{"webhooks":[` + e("****BBBB") + `,` + e("****AAAA") + `]}`, "secret-two-BBBB/token-B secret-one-AAAA/token-A"},
		{"round trip", `{"webhooks":[` + e("****AAAA") + `,` + e("****BBBB") + `]}`, "secret-one-AAAA/token-A secret-two-BBBB/token-B"},
	} {
		d := live()
		put(d, tc.body)
		if got := pairs(d); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
