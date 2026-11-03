package admin

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

func TestServerStartTLSFailureReleasesListener(t *testing.T) {
	malformedDir := t.TempDir()
	malformedCert := filepath.Join(malformedDir, "cert.pem")
	malformedKey := filepath.Join(malformedDir, "key.pem")
	if err := os.WriteFile(malformedCert, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(malformedKey, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		cert string
		key  string
	}{
		{name: "missing files", cert: filepath.Join(t.TempDir(), "missing-cert.pem"), key: filepath.Join(t.TempDir(), "missing-key.pem")},
		{name: "malformed files", cert: malformedCert, key: malformedKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := probe.Addr().String()
			if err := probe.Close(); err != nil {
				t.Fatal(err)
			}

			cfg := &config.Config{}
			cfg.Global.Admin.Listen = addr
			cfg.Global.Admin.APIKey = "strong-test-admin-key"
			cfg.Global.Admin.TLSCert = tc.cert
			cfg.Global.Admin.TLSKey = tc.key
			s := New(cfg, logger.New("error", "text"), metrics.New())
			if err := s.Start(); err == nil {
				t.Fatal("Start unexpectedly accepted invalid TLS material")
			}

			rebound, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatalf("TLS setup failure retained listener %s: %v", addr, err)
			}
			if err := rebound.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
