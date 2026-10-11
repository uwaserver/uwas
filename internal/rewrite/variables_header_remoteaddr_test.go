package rewrite

import (
	"net/http/httptest"
	"testing"
)

// F2890: %{REMOTE_ADDR} is the bare client address. The socket form carries a
// port, so an anchored IP condition never matched and an IP-based deny rule
// was silently inert.
func TestRemoteAddrIsBareAddress(t *testing.T) {
	cases := []struct{ remote, want string }{
		{"203.0.113.9:4000", "203.0.113.9"},
		{"203.0.113.9:0", "203.0.113.9"},
		{"[2001:db8::7]:443", "2001:db8::7"},
		{"203.0.113.9", "203.0.113.9"}, // no port: unchanged
		{"", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		v := BuildVariables(r, "/root", "/root/x", false)
		if got := v.Expand("%{REMOTE_ADDR}"); got != c.want {
			t.Errorf("REMOTE_ADDR for %q = %q, want %q", c.remote, got, c.want)
		}
	}
	cond, err := ParseCondition("%{REMOTE_ADDR}", `^203\.0\.113\.9$`, "")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	if ok, _ := cond.Evaluate(BuildVariables(r, "/root", "/root/x", false)); !ok {
		t.Error("anchored REMOTE_ADDR condition did not match the client")
	}
}

// F2891: %{HTTP:Name} and the common HTTP_* variables used to expand to the
// empty string, so a condition keyed on them never matched (and a negated one
// always did).
func TestHeaderVariables(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Cookie", "a=1; b=2")
	r.Header.Set("Accept", "text/html")
	r.Header.Add("X-Multi", "one")
	r.Header.Add("X-Multi", "two")
	v := BuildVariables(r, "/root", "/root/x", true)
	for expr, want := range map[string]string{
		"%{HTTP:X-Forwarded-Proto}": "https",
		"%{HTTP:x-forwarded-proto}": "https",
		"%{HTTP_COOKIE}":            "a=1; b=2",
		"%{HTTP_ACCEPT}":            "text/html",
		"%{HTTP:X-Multi}":           "one, two",
		"%{HTTP:X-Absent}":          "",
		"%{HTTP:}":                  "",
		"%{REQUEST_SCHEME}":         "https",
		"%{SERVER_PROTOCOL}":        "HTTP/1.1",
		"%{NO_SUCH_VARIABLE}":       "", // unknown stays empty
	} {
		if got := v.Expand(expr); got != want {
			t.Errorf("%s = %q, want %q", expr, got, want)
		}
	}
	// The usual "force https behind a proxy" condition must stop firing once
	// the proxy says the request already was https.
	cond, err := ParseCondition("%{HTTP:X-Forwarded-Proto}", "!https", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := cond.Evaluate(v); ok {
		t.Error("!https matched a request that carried X-Forwarded-Proto: https")
	}
	plain := BuildVariables(httptest.NewRequest("GET", "/", nil), "/root", "/root/x", false)
	if ok, _ := cond.Evaluate(plain); !ok {
		t.Error("!https did not match a request without the header")
	}
	if got := plain.Expand("%{REQUEST_SCHEME}"); got != "http" {
		t.Errorf("REQUEST_SCHEME = %q, want http", got)
	}
}
