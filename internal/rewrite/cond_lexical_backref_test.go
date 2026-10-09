package rewrite

import "testing"

// TestHTTPSForceBlockDoesNotLoopOnHTTPS pins Apache's lexical "!=on"
// CondPattern. Compiled as a regex, "=on" never matched the value "on", so the
// standard HTTPS-forcing block redirected HTTPS requests to themselves forever.
func TestHTTPSForceBlockDoesNotLoopOnHTTPS(t *testing.T) {
	cond, err := ParseCondition("%{HTTPS}", "!=on", "")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := ParseRule("^", "https://example.com/", "L,R=301")
	if err != nil {
		t.Fatal(err)
	}
	rule.Conditions = []Condition{*cond}
	engine := NewEngine([]*Rule{rule})

	if r := engine.Process("/x", "", &Variables{RequestURI: "/x", HTTPS: "on"}); r.Redirect {
		t.Errorf("HTTPS=on: redirected to %q, want no redirect", r.URI)
	}
	if r := engine.Process("/x", "", &Variables{RequestURI: "/x", HTTPS: "off"}); !r.Redirect {
		t.Error("HTTPS=off: expected redirect")
	}
}

// TestNegatedConditionKeepsBackreferences pins that %N refers to the last
// matched regex condition. A negated condition used to reset the captures, so
// "https://%1/$1" became "https:///evil.com", which browsers resolve to the
// host evil.com — an open redirect.
func TestNegatedConditionKeepsBackreferences(t *testing.T) {
	host, err := ParseCondition("%{HTTP_HOST}", `^www\.(.+)$`, "")
	if err != nil {
		t.Fatal(err)
	}
	wellKnown, err := ParseCondition("%{REQUEST_URI}", `!^/\.well-known/`, "")
	if err != nil {
		t.Fatal(err)
	}
	notFile, err := ParseCondition("%{REQUEST_FILENAME}", "!-f", "")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := ParseRule("^/(.*)$", "https://%1/$1", "R=301,L")
	if err != nil {
		t.Fatal(err)
	}
	rule.Conditions = []Condition{*host, *wellKnown, *notFile}

	r := NewEngine([]*Rule{rule}).Process("/evil.com", "", &Variables{
		HTTPHost:        "www.site.com",
		RequestURI:      "/evil.com",
		RequestFilename: "/nonexistent/evil.com",
	})
	if want := "https://site.com/evil.com"; r.URI != want {
		t.Errorf("redirect = %q, want %q", r.URI, want)
	}
}
