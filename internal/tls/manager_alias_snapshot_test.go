package uwastls

import (
	"crypto/tls"
	"fmt"
	"github.com/uwaserver/uwas/internal/config"
	"testing"
)

func managedAliasCase(t *testing.T, kind string) uint16 {
	t.Helper()
	domains := []config.Domain{{Host: "original.test", Aliases: []string{"alias.test"}, SSL: config.SSLConfig{MinVersion: "1.3"}}}
	m := testManager(t, domains)
	if kind == "update" {
		domains = []config.Domain{{Host: "original.test", Aliases: []string{"alias.test"}, SSL: config.SSLConfig{MinVersion: "1.3"}}}
		m.UpdateDomains(domains)
	}
	if kind == "snapshot" {
		domains = m.snapshotDomains()
	}
	base := m.TLSConfig()
	control, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "original.test"})
	if err != nil || control == nil || control.MinVersion != tls.VersionTLS13 {
		t.Fatal("control", err)
	}
	fmt.Printf("CONTROL EXPECTED: %x ACTUAL: %x\n", tls.VersionTLS13, control.MinVersion)
	release, done := make(chan struct{}), make(chan struct{})
	go func() { <-release; domains[0].Aliases[0] = "changed.test"; close(done) }()
	close(release)
	<-done
	out, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "alias.test"})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		return base.MinVersion
	}
	return out.MinVersion
}
func TestTLSDomainAliasSnapshotOwnership(t *testing.T) {
	for _, kind := range []string{"new", "update", "snapshot"} {
		if got := managedAliasCase(t, kind); got != tls.VersionTLS13 {
			t.Fatal(kind, got)
		}
	}
	m := testManager(t, nil)
	m.UpdateDomains(nil)
	if len(m.snapshotDomains()) != 0 {
		t.Fatal("empty snapshot")
	}
}
