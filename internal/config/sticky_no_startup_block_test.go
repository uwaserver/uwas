package config

import (
	"strings"
	"testing"
)

// proxy.sticky was ignored until now, so a config already carrying an odd
// type has been running fine. Rejecting it at load would turn an upgrade into
// a server that will not start — Load() calls Validate and serve refuses to
// run on the error. The runtime falls back to the configured algorithm and
// logs, which is the right place to report it.
func TestUnknownStickyTypeDoesNotBlockStartup(t *testing.T) {
	for _, tip := range []string{"nonsense", "COOKIE ", "session", ""} {
		cfg := &Config{
			Global: GlobalConfig{LogLevel: "info", LogFormat: "text"},
			Domains: []Domain{{
				Host: "p.test",
				Type: "proxy",
				SSL:  SSLConfig{Mode: "off"},
				Proxy: ProxyConfig{
					Upstreams: []Upstream{{Address: "http://127.0.0.1:9000", Weight: 1}},
					Algorithm: "round_robin",
					Sticky:    StickyConfig{Type: tip, TTL: -1},
				},
			}},
		}
		if err := Validate(cfg); err != nil {
			if strings.Contains(err.Error(), "sticky") {
				t.Errorf("sticky.type=%q blocked startup:\n%v", tip, err)
			} else {
				t.Fatalf("the fixture is unexpectedly invalid:\n%v", err)
			}
		}
	}
}
