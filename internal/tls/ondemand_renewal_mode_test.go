package uwastls

// F47300: with acme.on_demand enabled, a handshake for a configured host whose
// ssl.mode is "off" (the default) gets an ACME certificate. checkRenewals then
// treated that certificate as operator-supplied (alert only), so it was never
// renewed: it expired after 90 days and a restart loaded the expired pair and
// kept serving it, because a cached certificate suppresses a new on-demand
// order. A certificate the manager itself obtained is renewed; one the
// operator supplied (ssl.mode manual) and one that exists with on_demand off
// stay alert-only.

import (
	"context"
	"crypto/tls"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func renewalProbe(t *testing.T, mode string, onDemand bool) (calls *atomic.Int64, renewed, alerts *atomic.Int64) {
	t.Helper()
	domains := []config.Domain{{Host: "od.test", SSL: config.SSLConfig{Mode: mode}}}
	m := NewManager(config.ACMEConfig{Email: "ops@example.com", OnDemand: onDemand, Storage: t.TempDir()},
		domains, logger.New("error", "text"))
	calls, renewed, alerts = &atomic.Int64{}, &atomic.Int64{}, &atomic.Int64{}
	m.acmeObtainFunc = func(ctx context.Context, names []string) (*tls.Certificate, []byte, []byte, error) {
		calls.Add(1)
		c, certPEM, keyPEM := generateTestCertWithExpiry(t, names[0], 90*24*time.Hour)
		return c, certPEM, keyPEM, nil
	}
	m.SetOnCertRenewed(func(string) { renewed.Add(1) })
	m.SetOnCertExpiry(func(string, int) { alerts.Add(1) })
	// The certificate in use is 5 days from expiry.
	m.certs.Store("od.test", generateCertWithExpiry(t, "od.test", 5*24*time.Hour))
	m.checkRenewals(context.Background())
	return calls, renewed, alerts
}

func TestOnDemandCertificateRenewsUnderSSLModeOff(t *testing.T) {
	t.Run("ssl.mode off with on_demand: renewed", func(t *testing.T) {
		calls, renewed, _ := renewalProbe(t, "off", true)
		t.Logf("EXPECTED: 1 ACME order and a renewal\nACTUAL: orders=%d renewed=%d", calls.Load(), renewed.Load())
		if calls.Load() != 1 || renewed.Load() != 1 {
			t.Fatalf("PROBLEM CONFIRMED: orders=%d renewed=%d", calls.Load(), renewed.Load())
		}
	})
	t.Run("control: ssl.mode off without on_demand stays alert-only", func(t *testing.T) {
		calls, _, alerts := renewalProbe(t, "off", false)
		if calls.Load() != 0 || alerts.Load() != 1 {
			t.Errorf("orders=%d alerts=%d, want 0 and 1", calls.Load(), alerts.Load())
		}
	})
	t.Run("control: ssl.mode manual with on_demand stays alert-only", func(t *testing.T) {
		calls, _, alerts := renewalProbe(t, "manual", true)
		if calls.Load() != 0 || alerts.Load() != 1 {
			t.Errorf("orders=%d alerts=%d, want 0 and 1", calls.Load(), alerts.Load())
		}
	})
	t.Run("control: ssl.mode auto renews as before", func(t *testing.T) {
		calls, renewed, _ := renewalProbe(t, "auto", false)
		if calls.Load() != 1 || renewed.Load() != 1 {
			t.Errorf("orders=%d renewed=%d, want 1 and 1", calls.Load(), renewed.Load())
		}
	})
}
