package htaccess

import (
	"net"
	"strings"
	"testing"
)

func accessFor(t *testing.T, src, method string) AccessDecision {
	t.Helper()
	ds, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return Convert(ds).AccessForMethod(net.ParseIP("203.0.113.9"), method)
}

// F2892: <Limit>/<LimitExcept> sections were ignored, so the common "block
// every method but GET/POST" and "protect POST" directives did nothing.
func TestLimitSectionsApplyByMethod(t *testing.T) {
	cases := []struct {
		name, src, method string
		want              AccessDecision
	}{
		{"LimitExcept denies PUT", "<LimitExcept GET POST>\nRequire all denied\n</LimitExcept>\n", "PUT", AccessDenied},
		{"LimitExcept spares GET", "<LimitExcept GET POST>\nRequire all denied\n</LimitExcept>\n", "GET", AccessUnset},
		{"LimitExcept spares POST", "<LimitExcept GET POST>\nRequire all denied\n</LimitExcept>\n", "POST", AccessUnset},
		{"GET covers HEAD (spared)", "<LimitExcept GET POST>\nRequire all denied\n</LimitExcept>\n", "HEAD", AccessUnset},
		{"Limit denies GET", "<Limit GET>\nRequire all denied\n</Limit>\n", "GET", AccessDenied},
		{"Limit GET covers HEAD", "<Limit GET>\nRequire all denied\n</Limit>\n", "HEAD", AccessDenied},
		{"Limit leaves POST", "<Limit GET>\nRequire all denied\n</Limit>\n", "POST", AccessUnset},
		{"Limit lower-case args", "<Limit post put>\nRequire all denied\n</Limit>\n", "PUT", AccessDenied},
		{"legacy Deny in Limit", "<Limit DELETE>\nOrder deny,allow\nDeny from all\n</Limit>\n", "DELETE", AccessDenied},
		{"outer grant, LimitExcept denies DELETE", "Require all granted\n<LimitExcept GET>\nRequire all denied\n</LimitExcept>\n", "DELETE", AccessDenied},
		{"outer grant, LimitExcept spares GET", "Require all granted\n<LimitExcept GET>\nRequire all denied\n</LimitExcept>\n", "GET", AccessGranted},
		{"outer deny, Limit grants GET", "Require all denied\n<Limit GET>\nRequire all granted\n</Limit>\n", "GET", AccessGranted},
		{"inside IfModule", "<IfModule mod_authz_core.c>\n<LimitExcept GET>\nRequire all denied\n</LimitExcept>\n</IfModule>\n", "PUT", AccessDenied},
		{"unknown method skips sections", "<LimitExcept GET>\nRequire all denied\n</LimitExcept>\n", "", AccessUnset},
	}
	for _, c := range cases {
		if got := accessFor(t, c.src, c.method); got != c.want {
			t.Errorf("%s: %s %q = %v, want %v", c.name, c.method, c.src, got, c.want)
		}
	}
	// AccessFor (no method) is unchanged.
	ds, _ := Parse(strings.NewReader("<LimitExcept GET>\nRequire all denied\n</LimitExcept>\n"))
	if got := Convert(ds).AccessFor(net.ParseIP("203.0.113.9")); got != AccessUnset {
		t.Errorf("AccessFor without a method = %v, want AccessUnset", got)
	}
}
