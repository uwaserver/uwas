package server

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/handler/proxy"
	"github.com/uwaserver/uwas/internal/logger"
)

// The balancer was built from d.Proxy.Algorithm alone, so the domain's
// proxy.sticky block never reached it. These tests guard the wiring itself:
// without them, repointing the call site back to NewBalancer would leave the
// proxy package's own tests green.

func stickyServer(t *testing.T, p config.ProxyConfig) *Server {
	t.Helper()

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:  "proxy.test",
			Type:  "proxy",
			SSL:   config.SSLConfig{Mode: "off"},
			Proxy: p,
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s
}

func stickyProxyConfig() config.ProxyConfig {
	return config.ProxyConfig{
		Upstreams: []config.Upstream{{Address: "http://127.0.0.1:9101", Weight: 1}},
		Algorithm: "least_conn",
		Sticky:    config.StickyConfig{Type: "cookie", CookieName: "UWAS_UPSTREAM", TTL: 600},
	}
}

func TestServerBuildsBalancerFromStickyConfig(t *testing.T) {
	s := stickyServer(t, stickyProxyConfig())

	b := s.proxyBalancers["proxy.test"]
	sb, ok := b.(*proxy.StickyBalancer)
	if !ok {
		t.Fatalf("balancer = %T, want *proxy.StickyBalancer — proxy.sticky sunucuya ulaşmıyor", b)
	}
	if sb.CookieName != "UWAS_UPSTREAM" || sb.TTL != 600 {
		t.Errorf("sticky ayarları taşınmadı: %q / %d", sb.CookieName, sb.TTL)
	}
	if _, ok := sb.Fallback.(*proxy.LeastConn); !ok {
		t.Errorf("fallback = %T, want *proxy.LeastConn", sb.Fallback)
	}
}

// The reload path builds its own balancer map and must not lose the block.
func TestReloadRebuildsBalancerFromStickyConfig(t *testing.T) {
	s := stickyServer(t, config.ProxyConfig{
		Upstreams: []config.Upstream{{Address: "http://127.0.0.1:9101", Weight: 1}},
		Algorithm: "round_robin",
	})

	if _, ok := s.proxyBalancers["proxy.test"].(*proxy.StickyBalancer); ok {
		t.Fatal("sticky bloğu yokken StickyBalancer üretildi")
	}

	newCfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:  "proxy.test",
			Type:  "proxy",
			SSL:   config.SSLConfig{Mode: "off"},
			Proxy: stickyProxyConfig(),
		}},
	}
	s.rebuildProxyPools(newCfg.Domains)

	sb, ok := s.proxyBalancers["proxy.test"].(*proxy.StickyBalancer)
	if !ok {
		t.Fatalf("reload sonrası balancer = %T — sticky bloğu reload yolunda düşüyor", s.proxyBalancers["proxy.test"])
	}
	if sb.CookieName != "UWAS_UPSTREAM" || sb.TTL != 600 {
		t.Errorf("reload sticky ayarlarını taşımadı: %q / %d", sb.CookieName, sb.TTL)
	}
}
