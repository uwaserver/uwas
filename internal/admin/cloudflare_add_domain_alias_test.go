package admin

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F24900: AddDomainIfAbsent and ExistingDomains must treat aliases and the
// www. twin of a configured domain as already present.
func TestCFDepsAddDomainIfAbsentAliasAndTwin(t *testing.T) {
	s := &Server{config: &config.Config{Domains: []config.Domain{
		{Host: "example.com", Aliases: []string{"shop.example.org"}},
	}}}
	d := &cfDeps{s: s}
	for _, host := range []string{"shop.example.org", "SHOP.example.org", "www.example.com"} {
		if d.AddDomainIfAbsent(config.Domain{Host: host}) {
			t.Errorf("%s added although it is an alias/twin of example.com", host)
		}
	}
	if !d.AddDomainIfAbsent(config.Domain{Host: "other.example.net"}) {
		t.Error("unrelated host rejected")
	}
	ex := d.ExistingDomains()
	if !ex["shop.example.org"] {
		t.Error("ExistingDomains misses alias shop.example.org")
	}
}
