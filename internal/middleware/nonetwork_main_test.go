package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
)

// TestMain refuses every non-loopback dial made through http.DefaultTransport.
// GeoIP tests that send a public client IP queue a background ip-api.com
// lookup (lookupExternal); without this guard the package tests make real
// outbound requests. Refused lookups return "", the same as an offline host,
// which is what the assertions already expect. The transport stays a
// *http.Transport so code that clones it still works.
func TestMain(m *testing.M) {
	t := http.DefaultTransport.(*http.Transport)
	base := t.DialContext
	if base == nil {
		base = (&net.Dialer{}).DialContext
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
				return base(ctx, network, addr)
			}
		}
		return nil, fmt.Errorf("test: outbound network disabled (%s)", addr)
	}
	os.Exit(m.Run())
}
