package files

// Regression guard for domainrootFallback: its output feeds SFTP chroots and
// SSH-key paths (via ResolveSiteUserRoot), and it is reached precisely with
// the domains that domainroot.Fallback REJECTS as unsafe — the safe ones
// return earlier through Fallback itself. The inline copy therefore must make
// the same accept/reject decision: an unsafe domain must yield "" so the
// handlers answer 404 instead of provisioning outside the web root.

import (
	"testing"

	"github.com/uwaserver/uwas/internal/domainroot"
)

// TestDomainrootFallbackDecisionParityWithFallback pins that
// domainrootFallback accepts and rejects exactly the hosts
// domainroot.Fallback accepts and rejects. The function's own comment claims
// to match Fallback's logic; before the guard was added, a host like
// "../evil" produced "/var/www/../evil" — an escaped path — where Fallback
// returns "".
func TestDomainrootFallbackDecisionParityWithFallback(t *testing.T) {
	hosts := []string{
		"safe.example.com", // control: both must accept
		"../evil",          // traversal
		"a/b",              // separator
		`a\b`,              // Windows separator
		"a:b",              // drive-letter colon
		"..",               // bare traversal
		"",                 // empty
	}

	for _, host := range hosts {
		fallback := domainroot.Fallback("/var/www", host)
		inline := domainrootFallback("/var/www", host)

		if (fallback == "") != (inline == "") {
			t.Errorf("decision mismatch for host %q: domainroot.Fallback=%q, domainrootFallback=%q",
				host, fallback, inline)
		}
	}
}
