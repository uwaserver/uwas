package admin

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Admin handlers reach external services through clients that fall back to
// http.DefaultTransport (cloudflare.New, serverip.PublicIP, the WordPress
// downloader, the self-update check). installTestNetworkGuard makes that
// transport refuse every non-loopback dial, so the package tests never leave
// the machine. The transport stays a *http.Transport (monitor.New clones it)
// and clones inherit the guard. Tests that need an external host register a
// local fake with fakeExternalHost.

var (
	testNetMu        sync.Mutex
	testNetOverrides = map[string]string{} // "host:port" → fake server addr
)

func testNetLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func testNetOverride(addr string) (string, bool) {
	testNetMu.Lock()
	defer testNetMu.Unlock()
	to, ok := testNetOverrides[addr]
	return to, ok
}

func installTestNetworkGuard() {
	t := http.DefaultTransport.(*http.Transport)
	base := t.DialContext
	if base == nil {
		base = (&net.Dialer{}).DialContext
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if to, ok := testNetOverride(addr); ok {
			return base(ctx, network, to)
		}
		if host, _, err := net.SplitHostPort(addr); err == nil && testNetLoopback(host) {
			return base(ctx, network, addr)
		}
		return nil, fmt.Errorf("test: outbound network disabled (%s)", addr)
	}
	// HTTPS: a faked host is served by a plain-HTTP fake (the returned conn is
	// used as-is); loopback keeps a real TLS handshake; anything else refused.
	t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if to, ok := testNetOverride(addr); ok {
			return base(ctx, network, to)
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil || !testNetLoopback(host) {
			return nil, fmt.Errorf("test: outbound network disabled (%s)", addr)
		}
		conn, err := base(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		cfg := &tls.Config{}
		if t.TLSClientConfig != nil {
			cfg = t.TLSClientConfig.Clone()
		}
		if cfg.ServerName == "" {
			cfg.ServerName = host
		}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return tc, nil
	}
}

// fakeExternalHost serves hostport ("wordpress.org:443") from a local
// plain-HTTP handler for the duration of the test.
func fakeExternalHost(t *testing.T, hostport string, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	testNetMu.Lock()
	testNetOverrides[hostport] = srv.Listener.Addr().String()
	testNetMu.Unlock()
	t.Cleanup(func() {
		testNetMu.Lock()
		delete(testNetOverrides, hostport)
		testNetMu.Unlock()
		srv.Close()
	})
}

// installFakeUFW puts a `ufw` that always fails first on PATH, so handler
// paths that call internal/firewall directly (GET /firewall's default-deny
// heal) can never run the host's real ufw. Tests that need specific ufw
// output still set their own PATH.
func installFakeUFW() (cleanup func()) {
	dir, err := os.MkdirTemp("", "uwas-admin-fakebin-")
	if err != nil {
		panic(err)
	}
	script := "#!/bin/sh\necho 'ufw: disabled in admin tests' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "ufw"), []byte(script), 0o755); err != nil {
		panic(err)
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() { os.RemoveAll(dir) }
}
