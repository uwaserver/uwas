package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// F2890-F2892: security directives migrated from Apache that used to be
// silently inert — mod_rewrite conditions on %{REMOTE_ADDR} (anchored, the
// socket form carried a port), on %{HTTP:Header}/%{HTTP_COOKIE}, and
// <Limit>/<LimitExcept> sections.
func apacheSemanticsDo(t *testing.T, htaccess, method, remote string, hdr map[string]string) int {
	t.Helper()
	s := nonCanonServer(t, htaccess, "    htaccess: {mode: import}\n")
	req := httptest.NewRequest(method, "/index.html", nil)
	req.Host = "a.test"
	req.RemoteAddr = remote
	req.Header.Set("User-Agent", "uwas-test")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestHtaccessRewriteConditionVariables(t *testing.T) {
	deny := func(variable, pattern string) string {
		return fmt.Sprintf("RewriteEngine On\nRewriteCond %s %s\nRewriteRule .* - [F]\n", variable, pattern)
	}
	const in, out = "203.0.113.9:4000", "192.0.2.5:1"
	cases := []struct {
		name, ht, remote string
		hdr              map[string]string
		want             int
	}{
		{"control: UA", deny("%{HTTP_USER_AGENT}", "uwas-test"), out, nil, 403},
		{"anchored REMOTE_ADDR, that client", deny("%{REMOTE_ADDR}", `^203\.0\.113\.9$`), in, nil, 403},
		{"anchored REMOTE_ADDR, other client", deny("%{REMOTE_ADDR}", `^203\.0\.113\.9$`), out, nil, 200},
		{"allowlist (negated, anchored), allowed", deny("%{REMOTE_ADDR}", `!^192\.0\.2\.5$`), out, nil, 200},
		{"allowlist (negated, anchored), outsider", deny("%{REMOTE_ADDR}", `!^192\.0\.2\.5$`), in, nil, 403},
		{"IPv6 anchored", deny("%{REMOTE_ADDR}", `^2001:db8::7$`), "[2001:db8::7]:443", nil, 403},
		{"HTTP:Header present", deny("%{HTTP:X-Block}", "yes"), out, map[string]string{"X-Block": "yes"}, 403},
		{"HTTP:Header other value", deny("%{HTTP:X-Block}", "yes"), out, map[string]string{"X-Block": "no"}, 200},
		{"HTTP:Header absent", deny("%{HTTP:X-Block}", "yes"), out, nil, 200},
		{"HTTP_COOKIE", deny("%{HTTP_COOKIE}", "evil=1"), out, map[string]string{"Cookie": "a=b; evil=1"}, 403},
		{"HTTP_ACCEPT", deny("%{HTTP_ACCEPT}", "application/x-evil"), out, map[string]string{"Accept": "application/x-evil"}, 403},
		{"proxy https header suppresses the redirect rule", "RewriteEngine On\nRewriteCond %{HTTP:X-Forwarded-Proto} !https\nRewriteRule .* - [F]\n", out, map[string]string{"X-Forwarded-Proto": "https"}, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := apacheSemanticsDo(t, c.ht, "GET", c.remote, c.hdr); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestHtaccessLimitSectionsEnforced(t *testing.T) {
	except := "<LimitExcept GET HEAD POST>\nRequire all denied\n</LimitExcept>\n"
	only := "<Limit GET>\nRequire all denied\n</Limit>\n"
	cases := []struct {
		name, ht, method string
		denied           bool
	}{
		{"LimitExcept denies PUT", except, "PUT", true},
		{"LimitExcept denies DELETE", except, "DELETE", true},
		{"LimitExcept spares GET", except, "GET", false},
		{"LimitExcept spares HEAD", except, "HEAD", false},
		{"LimitExcept spares POST", except, "POST", false},
		{"Limit GET denies GET", only, "GET", true},
		{"Limit GET denies HEAD", only, "HEAD", true},
		{"Limit GET spares POST", only, "POST", false},
		{"outer grant, LimitExcept denies PUT", "Require all granted\n" + except, "PUT", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := apacheSemanticsDo(t, c.ht, c.method, "192.0.2.5:1", nil)
			if (got == http.StatusForbidden) != c.denied {
				t.Fatalf("%s = %d, denied want %v", c.method, got, c.denied)
			}
		})
	}
}
