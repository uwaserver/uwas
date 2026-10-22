package siteuser

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// listedDomains runs ListUsers over a fake /etc/passwd holding one account per
// (username, home) pair.
func listedDomains(t *testing.T, accounts [][2]string) map[string]string {
	t.Helper()
	snap := saveHooks()
	t.Cleanup(func() { restoreHooks(snap) })
	runtimeGOOS = "linux"
	var passwd strings.Builder
	for _, a := range accounts {
		fmt.Fprintf(&passwd, "%s:x:1001:1001::%s:/usr/sbin/nologin\n", a[0], a[1])
	}
	osStatFn = func(string) (os.FileInfo, error) { return nil, nil }
	osReadFileFn = func(string) ([]byte, error) { return []byte(passwd.String()), nil }
	out := map[string]string{}
	for _, u := range ListUsers() {
		out[u.Username] = u.Domain
	}
	return out
}

// F24701: ListUsers recovered the domain by undoing "." -> "--" on the account
// name, which is wrong for every hashed name (host too long, or containing
// "--"). The panel shows the garbled name and builds its "already has an SFTP
// user" set from it, so the real domain stays offered for creation.
func TestListUsersHashedAccountShowsRealDomain(t *testing.T) {
	const long = "very-long-subdomain-name.example.com"
	const short = "example.com"
	got := listedDomains(t, [][2]string{
		{domainToUsername(short), "/var/www/" + short},
		{domainToUsername(long), "/var/www/" + long},
	})
	// Unaffected control: a short host round-trips.
	if got[domainToUsername(short)] != short {
		t.Fatalf("control failed: %q", got[domainToUsername(short)])
	}
	listed := got[domainToUsername(long)]
	t.Logf("EXPECTED: listed domain %q", long)
	t.Logf("ACTUAL:   listed domain %q", listed)
	if listed != long {
		t.Fatal("PROBLEM CONFIRMED")
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

func TestDomainForAccount(t *testing.T) {
	const long = "very-long-subdomain-name.example.com"
	const dashed = "shop--x.example.com"
	for _, c := range []struct{ name, user, home, want string }{
		{"short host unchanged", domainToUsername("example.com"), "/var/www/example.com", "example.com"},
		{"hashed long host", domainToUsername(long), "/var/www/" + long, long},
		{"host with --", domainToUsername(dashed), "/var/www/" + dashed, dashed},
		{"legacy name of a -- host", legacyUsername(dashed), "/var/www/" + dashed, dashed},
		{"mixed-case dir", domainToUsername("Example.COM"), "/var/www/Example.COM", "Example.COM"},
		// The home names another host, or none at all: fall back to unpacking.
		{"home of another host", "uwas-example--com", "/var/www/other.com", "example.com"},
		{"custom home dir", "uwas-example--com", "/srv/sites/a1", "example.com"},
		{"no home", "uwas-example--com", "", "example.com"},
	} {
		if got := domainForAccount(c.user, c.home); got != c.want {
			t.Errorf("%s: domainForAccount(%q, %q) = %q, want %q", c.name, c.user, c.home, got, c.want)
		}
	}
}
