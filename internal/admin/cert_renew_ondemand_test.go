package admin

// F47301: with acme.on_demand enabled, a host whose ssl.mode is "off" gets an
// ACME certificate at its first handshake (and F47300 now renews it), but the
// Force Renew endpoint still refused it with 409 "not managed by ACME", so the
// operator could not renew the certificate the server itself obtained. A
// certificate the operator supplied (ssl.mode manual) stays refused, and so
// does "off" without on-demand.

import (
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	uwastls "github.com/uwaserver/uwas/internal/tls"
)

func TestCertRenewAllowedForOnDemandCertificateUnderModeOff(t *testing.T) {
	cases := []struct {
		name     string
		mode     string
		onDemand bool
		want     int
	}{
		// Without an ACME client the manager answers 500, so 500 proves the
		// handler let the request through and 409 that it stopped it.
		{"off with on_demand reaches the manager", "off", true, http.StatusInternalServerError},
		{"control: off without on_demand refused", "off", false, http.StatusConflict},
		{"control: manual with on_demand refused", "manual", true, http.StatusConflict},
		{"control: auto reaches the manager", "auto", false, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doms := []config.Domain{{Host: "site.test", Type: "static", SSL: config.SSLConfig{Mode: tc.mode}}}
			e := newHostConflictEnv(t, doms)
			e.s.configMu.Lock()
			e.s.config.Domains[0].SSL.Mode = tc.mode
			e.s.config.Global.ACME.OnDemand = tc.onDemand
			e.s.configMu.Unlock()
			e.s.SetTLSManager(uwastls.NewManager(config.ACMEConfig{Storage: t.TempDir(), OnDemand: tc.onDemand}, doms, testLogger(t)))
			code := e.do(http.MethodPost, "/api/v1/certs/site.test/renew", "", auth.RoleAdmin)
			t.Logf("EXPECTED: %d\nACTUAL: %d", tc.want, code)
			if code != tc.want {
				t.Fatalf("PROBLEM CONFIRMED: status = %d, want %d", code, tc.want)
			}
		})
	}
}
