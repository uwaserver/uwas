package siteuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlainNameNeverMatchesHashedAccount: a host spelled to equal another
// host's hashed account name ("uwas-<prefix>-<8 hex>") mapped onto that
// account, so deleting the crafted host deleted the victim's account and its
// authorized_keys (F68200).
func TestPlainNameNeverMatchesHashedAccount(t *testing.T) {
	for _, victim := range []string{"shop.storefront-production.com", "x--y.com"} {
		victimAcct := domainToUsername(victim)
		crafted := strings.ReplaceAll(strings.TrimPrefix(victimAcct, "uwas-"), "--", ".")
		if validateSiteHostname(crafted) != nil {
			t.Fatalf("crafted host %q is not valid; test setup broken", crafted)
		}
		if got := domainToUsername(crafted); got == victimAcct {
			t.Errorf("%q maps onto %q's account %q", crafted, victim, got)
		}

		home := filepath.Join(t.TempDir(), victim)
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		keys := filepath.Join(home, ".ssh", "authorized_keys")
		if err := os.WriteFile(keys, []byte("ssh-ed25519 AAAA victim\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		e := newCollisionEnv(t, passwdLine(victimAcct, home))
		if err := DeleteUser(crafted); err != nil {
			t.Fatal(err)
		}
		if e.ran("userdel " + victimAcct) {
			t.Errorf("DeleteUser(%q) deleted %q's account", crafted, victim)
		}
		if _, err := os.Stat(keys); err != nil {
			t.Errorf("DeleteUser(%q) removed %q's keys: %v", crafted, victim, err)
		}
	}
}

// TestHashLikeHostKeepsLegacyPlainAccount: an account created under the plain
// name before such hosts were hashed stays the host's own.
func TestHashLikeHostKeepsLegacyPlainAccount(t *testing.T) {
	host := "site.pr-20261122"
	legacy := legacyUsername(host)
	if domainToUsername(host) == legacy {
		t.Fatalf("hash-like host %q still gets the plain name", host)
	}
	home := filepath.Join(t.TempDir(), host)
	e := newCollisionEnv(t, passwdLine(legacy, home))
	if got := accountName(host, ""); got != legacy {
		t.Fatalf("accountName(%q) = %q, want legacy %q", host, got, legacy)
	}
	if err := DeleteUser(host); err != nil {
		t.Fatal(err)
	}
	if !e.ran("userdel " + legacy) {
		t.Error("legacy account of the hash-like host was orphaned")
	}
	if domainToUsername("example.com") != "uwas-example--com" {
		t.Error("plain host name changed")
	}
}
