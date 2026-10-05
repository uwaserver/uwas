package server

import (
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
	"net/http/httptest"
	"testing"
)

func redirectTargetComponentsRedirect(target, uri string, preserve bool, status int) (int, string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", uri, nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	s := &Server{}
	s.handleRedirect(ctx, &config.Domain{Redirect: config.RedirectConfig{Target: target, PreservePath: preserve, Status: status}})
	return rec.Code, rec.Header().Get("Location")
}
func TestRedirectPreservePathKeepsTargetComponents(t *testing.T) {
	for _, c := range []struct {
		name, target, uri, want string
		preserve                bool
		status                  int
	}{
		{"fragment", "https://destination.test/base#section", "/docs?q=1", "https://destination.test/base/docs?q=1#section", true, 0},
		{"query and fragment", "https://destination.test/base/?lang=en#section", "/docs?q=1", "https://destination.test/base/docs?lang=en&q=1#section", true, 302},
		{"target query only", "https://destination.test/base?lang=en", "/docs", "https://destination.test/base/docs?lang=en", true, 307},
		{"plain control", "https://destination.test/base/", "/docs?q=1", "https://destination.test/base/docs?q=1", true, 308},
		{"disabled preserve", "https://destination.test/base?lang=en#section", "/docs?q=1", "https://destination.test/base?lang=en#section", false, 302},
		{"encoded path", "https://destination.test/base%2Fdir/#section", "/a%2Fb?x=%2F", "https://destination.test/base%2Fdir/a%2Fb?x=%2F#section", true, 301},
		{"root and empty query", "https://destination.test/base#section", "/?", "https://destination.test/base/?#section", true, 301},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, loc := redirectTargetComponentsRedirect(c.target, c.uri, c.preserve, c.status)
			wantStatus := c.status
			if wantStatus == 0 {
				wantStatus = 301
			}
			if code != wantStatus || loc != c.want {
				t.Fatalf("EXPECTED: status=%d Location=%q ACTUAL: status=%d Location=%q", wantStatus, c.want, code, loc)
			}
		})
	}
}
