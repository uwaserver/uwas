package dnschecker

import (
	"net"
	"strings"
	"testing"
)

func checkWithCNAME(t *testing.T, domain string, cname func(string) (string, error)) Result {
	t.Helper()
	saveAndRestore(t)
	lookupHost = func(string) ([]string, error) { return []string{"203.0.113.9"}, nil }
	lookupCNAME = cname
	lookupMX = func(string) ([]*net.MX, error) { return nil, nil }
	lookupNS = func(string) ([]*net.NS, error) { return nil, nil }
	lookupTXT = func(string) ([]string, error) { return nil, nil }
	interfaceAddrs = func() ([]net.Addr, error) { return nil, nil }
	return Check(domain)
}

// The resolver returns the canonical lowercase FQDN; a domain typed with a
// trailing dot or mixed case without a CNAME must not be its own CNAME.
func TestCheckNoSelfCNAMEForFQDNAndMixedCase(t *testing.T) {
	canon := func(h string) (string, error) { return strings.ToLower(strings.TrimSuffix(h, ".")) + ".", nil }
	for _, d := range []string{"example.com", "example.com.", "Example.COM", "EXAMPLE.com."} {
		if got := checkWithCNAME(t, d, canon).CNAME; got != "" {
			t.Errorf("%q: CNAME=%q, want empty", d, got)
		}
	}
}

// A real alias is still reported for every spelling of the domain.
func TestCheckRealCNAMEStillReported(t *testing.T) {
	alias := func(string) (string, error) { return "alias.target.net.", nil }
	for _, d := range []string{"example.com", "example.com.", "Example.COM"} {
		if got := checkWithCNAME(t, d, alias).CNAME; got != "alias.target.net" {
			t.Errorf("%q: CNAME=%q", d, got)
		}
	}
}
