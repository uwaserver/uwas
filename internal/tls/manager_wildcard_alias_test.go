package uwastls

import (
	"crypto/tls"
	"github.com/uwaserver/uwas/internal/config"
	"testing"
)

func wildcardAliasConfig(t *testing.T, host string) uint16 {
	t.Helper()
	m := testManager(t, []config.Domain{{Host: "*.example.test", Aliases: []string{"alias.test"}, SSL: config.SSLConfig{MinVersion: "1.3"}}})
	base := m.TLSConfig()
	out, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: host})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		return base.MinVersion
	}
	return out.MinVersion
}
func TestWildcardAliasesRetainTLSVersion(t *testing.T) {
	for _, host := range []string{"sub.example.test", "example.test", "alias.test", "www.alias.test", "ALIAS.TEST."} {
		if got := wildcardAliasConfig(t, host); got != tls.VersionTLS13 {
			t.Fatalf("%s version=%x", host, got)
		}
	}
	for _, host := range []string{"unconfigured.test", ""} {
		if got := wildcardAliasConfig(t, host); got != tls.VersionTLS12 {
			t.Fatalf("unknown host=%s version=%x", host, got)
		}
	}
}
