package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// proxy.grpc was dead configuration: nothing read the field. The transport
// comment claimed "gRPC/h2c still relies on the same flag", but
// ForceAttemptHTTP2 only negotiates h2 over TLS via ALPN. A cleartext http://
// upstream still got HTTP/1.1, and gRPC does not run on HTTP/1.1 — so a
// domain configured for gRPC could not proxy it.

// protocolServer answers with the protocol it saw, over h2c.
func protocolServer(t *testing.T) *httptest.Server {
	t.Helper()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, r.Proto)
	})
	// Serves both HTTP/1.1 and cleartext HTTP/2 on one port, so the same
	// fixture shows the difference the flag makes.
	srv := httptest.NewUnstartedServer(h)
	var protos http.Protocols
	protos.SetHTTP1(true)
	protos.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &protos
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func grpcDomain(host string, upstream string, grpc bool) *config.Domain {
	return &config.Domain{
		Host: host,
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Upstreams:             []config.Upstream{{Address: upstream, Weight: 1}},
			GRPC:                  grpc,
			AllowPrivateUpstreams: true,
		},
	}
}

// roundTripProto round-trips one request through the domain's transport and
// returns the protocol the upstream reported.
func roundTripProto(t *testing.T, h *Handler, d *config.Domain, url string) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	resp, err := h.getTransport(d).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	return string(body)
}

func testHandler() *Handler { return New(logger.New("error", "text")) }

func TestGRPCUsesH2CForCleartextUpstream(t *testing.T) {
	up := protocolServer(t)
	h := testHandler()

	got := roundTripProto(t, h, grpcDomain("grpc.test", up.URL, true), up.URL+"/")
	if got != "HTTP/2.0" {
		t.Errorf("the upstream saw %q, want HTTP/2.0 — proxy.grpc does not set up h2c", got)
	}
}

// Without the flag the cleartext upstream must stay on HTTP/1.1: h2c has no
// negotiation, so speaking HTTP/2 to a server that does not expect it would
// break every plain proxy domain.
func TestWithoutGRPCCleartextStaysHTTP11(t *testing.T) {
	up := protocolServer(t)
	h := testHandler()

	got := roundTripProto(t, h, grpcDomain("plain.test", up.URL, false), up.URL+"/")
	if got != "HTTP/1.1" {
		t.Errorf("the upstream saw %q, want HTTP/1.1", got)
	}
}

// The transport cache must not hand a gRPC domain's h2c transport to a
// non-gRPC domain, or vice versa.
func TestGRPCTransportNotSharedAcrossDomains(t *testing.T) {
	up := protocolServer(t)
	h := testHandler()

	// Same host and timeouts; only the grpc flag differs.
	withGRPC := grpcDomain("same.test", up.URL, true)
	without := grpcDomain("same.test", up.URL, false)

	if got := roundTripProto(t, h, withGRPC, up.URL+"/"); got != "HTTP/2.0" {
		t.Errorf("the grpc domain saw %q", got)
	}
	if got := roundTripProto(t, h, without, up.URL+"/"); got != "HTTP/1.1" {
		t.Errorf("the non-grpc domain saw %q — the cache shared the h2c transport", got)
	}
}

// ResetTransports must close both halves without panicking on the wrapper.
func TestResetTransportsHandlesGRPCWrapper(t *testing.T) {
	up := protocolServer(t)
	h := testHandler()

	roundTripProto(t, h, grpcDomain("grpc.test", up.URL, true), up.URL+"/")
	h.ResetTransports()

	sayac := 0
	h.transports.Range(func(_, _ any) bool { sayac++; return true })
	if sayac != 0 {
		t.Errorf("%d transports remained after ResetTransports", sayac)
	}
}
