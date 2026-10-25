package fastcgi

import (
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

// Several Cookie header lines (HTTP/1.1 clients and proxies may split them) were
// joined with ", ". PHP separates cookies at ";" only, so the second line's
// first cookie was swallowed into the previous cookie's value and never
// reached $_COOKIE. RFC 6265 §5.4 and the HTTP/2 and HTTP/3 stacks join with
// "; "; other repeated headers keep ", " (F26101).
func TestBuildEnvJoinsCookieLinesWithSemicolon(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php", nil)
	r.Header["Cookie"] = []string{"a=1", "b=2"}
	r.Header["X-Multi"] = []string{"x", "y"}
	r.Header["Accept-Language"] = []string{"en", "tr"}
	ctx := router.AcquireContext(httptest.NewRecorder(), r)
	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil, 0)
	router.ReleaseContext(ctx)

	failed := false
	for _, c := range []struct{ key, want string }{
		{"HTTP_X_MULTI", "x, y"},
		{"HTTP_ACCEPT_LANGUAGE", "en, tr"},
		{"HTTP_COOKIE", "a=1; b=2"},
	} {
		t.Logf("%s: EXPECTED %q ACTUAL %q", c.key, c.want, env[c.key])
		if env[c.key] != c.want {
			failed = true
			t.Errorf("%s = %q, want %q", c.key, env[c.key], c.want)
		}
	}
	if failed {
		t.Log("PROBLEM CONFIRMED")
	} else {
		t.Log("PROBLEM NOT REPRODUCED")
	}
}
