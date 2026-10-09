package fastcgi

import (
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

// TestBuildEnvDropsUnderscoreHeaders is the regression for header smuggling
// through "_" spellings: X_Forwarded_For and X-Forwarded-For both map to
// HTTP_X_FORWARDED_FOR, so a client could add the underscore form next to
// the one a trusted proxy set and win whenever map order favoured it.
func TestBuildEnvDropsUnderscoreHeaders(t *testing.T) {
	for i := 0; i < 100; i++ {
		r := httptest.NewRequest("GET", "/index.php", nil)
		r.Header["Cf-Connecting-Ip"] = []string{"203.0.113.7"}
		r.Header["Cf_connecting_ip"] = []string{"6.6.6.6"}
		r.Header["X_forwarded_proto"] = []string{"https"}
		ctx := router.AcquireContext(httptest.NewRecorder(), r)
		env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil, 0)
		router.ReleaseContext(ctx)

		if got := env["HTTP_CF_CONNECTING_IP"]; got != "203.0.113.7" {
			t.Fatalf("run %d: HTTP_CF_CONNECTING_IP = %q, want the proxy-set 203.0.113.7", i, got)
		}
		if v, ok := env["HTTP_X_FORWARDED_PROTO"]; ok {
			t.Fatalf("run %d: underscore header forwarded as HTTP_X_FORWARDED_PROTO=%q", i, v)
		}
	}
}

// TestBuildEnvServerNameExcludesPort pins SERVER_NAME to the host name only,
// matching Apache and the rewrite engine's %{SERVER_NAME}; the port belongs
// in SERVER_PORT.
func TestBuildEnvServerNameExcludesPort(t *testing.T) {
	cases := []struct{ host, name, port string }{
		{"site.example:8443", "site.example", "8443"},
		{"site.example", "site.example", "80"},
		{"[2001:db8::1]:8080", "[2001:db8::1]", "8080"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/index.php", nil)
		r.Host = c.host
		ctx := router.AcquireContext(httptest.NewRecorder(), r)
		env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil, 0)
		router.ReleaseContext(ctx)

		if env["SERVER_NAME"] != c.name || env["SERVER_PORT"] != c.port {
			t.Errorf("host %q: SERVER_NAME=%q SERVER_PORT=%q, want %q %q", c.host, env["SERVER_NAME"], env["SERVER_PORT"], c.name, c.port)
		}
	}
}
