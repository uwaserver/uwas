package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F60600: attaching the site's own "www." name as an unknown-host alias looked
// the target up by the first record whose www-stripped name matched, so a
// "www." redirect record listed before the site was the one cleaned up and the
// site kept the stale alias.
func TestUnknownAliasSameSiteWWWCleansSiteNotRedirectRecord(t *testing.T) {
	for _, order := range []string{"redirect-first", "site-first"} {
		t.Run(order, func(t *testing.T) {
			s := testServer()
			s.config.Global.WebRoot = t.TempDir()
			d := wwwShadowDomains()
			d[1].Aliases = []string{"www.example.test"}
			if order == "redirect-first" {
				s.config.Domains = []config.Domain{d[0], d[1]}
			} else {
				s.config.Domains = []config.Domain{d[1], d[0]}
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/unknown-domains/www.example.test/alias",
				strings.NewReader(`{"domain":"example.test"}`))
			req.SetPathValue("host", "www.example.test")
			s.handleUnknownDomainsAlias(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
			}
			for _, dom := range s.config.Domains {
				if dom.Host == "example.test" && len(dom.Aliases) != 0 {
					t.Fatalf("EXPECTED: site alias list cleaned; ACTUAL: %v", dom.Aliases)
				}
			}
		})
	}
}
