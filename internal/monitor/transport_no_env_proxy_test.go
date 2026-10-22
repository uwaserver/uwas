package monitor

import (
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// The probe transport must not inherit ProxyFromEnvironment from
// http.DefaultTransport: behind HTTP_PROXY the dial-time SSRF policy would only
// see the proxy's address, never the tenant-controlled host being probed.
// (net/http reads the proxy environment once per process, so the transport
// field is asserted rather than a live proxy round trip.)
func TestNewTransportDoesNotUseEnvProxy(t *testing.T) {
	m := New(nil, logger.New("error", "text"))
	tr, ok := m.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", m.client.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("monitor transport inherits a proxy func; probes would bypass the dial-time SSRF policy")
	}
	if tr.DialContext == nil {
		t.Fatal("monitor transport lost its SSRF-controlled dialer")
	}
}
