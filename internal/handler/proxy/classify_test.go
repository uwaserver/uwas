package proxy

import (
	"errors"
	"strings"
	"testing"
)

// TestClassifyUpstreamErr locks in the operator-facing labels for the most
// common upstream failure shapes. The exact wording can be tweaked, but the
// CATEGORY each error maps into is part of the 502 contract: an operator
// reading `curl -v` on a broken proxy needs the body to point at TLS vs DNS
// vs timeout vs connection reset, not just "upstream connection failed".
func TestClassifyUpstreamErr(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		match string
	}{
		{"nil", nil, "no upstream"},
		{"x509 unknown auth", errors.New(`Get "https://x": x509: certificate signed by unknown authority`), "TLS certificate"},
		{"cert hostname mismatch", errors.New(`x509: certificate is valid for *.foo, not bar`), "TLS certificate"},
		{"tls handshake", errors.New(`remote error: tls: handshake failure`), "TLS"},
		{"dns no such host", errors.New(`dial tcp: lookup nope.invalid: no such host`), "DNS"},
		{"connection refused", errors.New(`dial tcp 127.0.0.1:9: connect: connection refused`), "refused"},
		{"connection reset", errors.New(`read tcp: connection reset by peer`), "reset"},
		{"i/o timeout", errors.New(`dial tcp 1.2.3.4:443: i/o timeout`), "timed out"},
		{"deadline exceeded", errors.New(`net/http: request canceled (context deadline exceeded)`), "timed out"},
		{"unreachable", errors.New(`dial tcp: network is unreachable`), "unreachable"},
		{"http2 protocol", errors.New(`http2: server sent unexpected SETTINGS frame`), "HTTP/2"},
		{"eof", errors.New(`unexpected EOF`), "prematurely"},
		{"unknown", errors.New(`weird transport error`), "upstream connection failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyUpstreamErr(c.err)
			if !strings.Contains(strings.ToLower(got), strings.ToLower(c.match)) {
				t.Errorf("classifyUpstreamErr(%v) = %q, want substring %q", c.err, got, c.match)
			}
		})
	}
}
