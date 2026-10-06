package proxy

import (
	"net"
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// privateRFC1918IPv4 finds a bindable RFC1918 address on the host (10/8,
// 172.16/12, 192.168/16). Skips the test when the environment only offers
// loopback — the loopback allow case is covered by the proxy's documented
// IsProxyUpstreamSafe policy.
func privateRFC1918IPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("interface addresses unavailable: %v", err)
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		if ip4[0] == 10 || (ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) || (ip4[0] == 192 && ip4[1] == 168) {
			if !ip4.IsLoopback() {
				return ip4.String()
			}
		}
	}
	t.Skip("no RFC1918 interface address available on this host")
	return ""
}

// TestHealthCheckerRejectsPrivateByDefault pins the default half of the
// allow_private_upstreams contract: without the opt-in, a health check on a
// private (non-loopback) backend must keep failing, exactly like the proxy
// path rejects it.
func TestHealthCheckerRejectsPrivateByDefault(t *testing.T) {
	ip := privateRFC1918IPv4(t)

	lis, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Skipf("cannot bind %s: %v", ip, err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(lis) }()
	defer func() { _ = srv.Close() }()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://" + lis.Addr().String(), Weight: 1}})
	b := pool.All()[0]

	hc := NewHealthChecker(pool, HealthConfig{Path: "/", Threshold: 1, Rise: 1}, logger.New("error", "text"))
	for i := 0; i < 3; i++ {
		hc.checkOne(b)
	}
	if b.IsHealthy() {
		t.Fatal("private backend became healthy although allow_private_upstreams is not enabled for the health checker")
	}
}

// TestHealthCheckerHonorsAllowPrivateOptIn pins the other half: with the
// domain's opt-in plumbed through (HealthConfig.AllowPrivate), the same
// private backend must turn healthy — the proxy path already serves it.
func TestHealthCheckerHonorsAllowPrivateOptIn(t *testing.T) {
	ip := privateRFC1918IPv4(t)

	lis, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Skipf("cannot bind %s: %v", ip, err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(lis) }()
	defer func() { _ = srv.Close() }()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://" + lis.Addr().String(), Weight: 1}})
	b := pool.All()[0]

	hc := NewHealthChecker(pool, HealthConfig{Path: "/", Threshold: 1, Rise: 1, AllowPrivate: true}, logger.New("error", "text"))
	for i := 0; i < 3; i++ {
		hc.checkOne(b)
	}
	if !b.IsHealthy() {
		t.Fatal("opted-in private backend never became healthy: AllowPrivate is not honored by checkOne")
	}
}

// keep the config import meaningful if future tests only use the policy docs
var _ = config.IsProxyUpstreamSafe
