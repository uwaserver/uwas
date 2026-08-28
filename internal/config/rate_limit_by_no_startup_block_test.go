package config

import (
	"strings"
	"testing"
)

// security.rate_limit.by was ignored until now, so a config already carrying
// an odd value has been running fine. Rejecting it at load would turn an
// upgrade into a server that will not start: Load() calls Validate and serve
// refuses to run on the error. The limiter falls back to the client address
// and the server warns once when it builds the limiter.
func TestUnknownRateLimitByDoesNotBlockStartup(t *testing.T) {
	for _, by := range []string{"cookie:sid", "header:", "header", "saçmalık", ""} {
		cfg := &Config{
			Global: GlobalConfig{LogLevel: "info", LogFormat: "text"},
			Domains: []Domain{{
				Host:     "rl.test",
				Type:     "static",
				Root:     "/srv/www",
				SSL:      SSLConfig{Mode: "off"},
				Security: SecurityConfig{RateLimit: RateLimitConfig{Requests: 10, By: by}},
			}},
		}
		if err := Validate(cfg); err != nil {
			if strings.Contains(err.Error(), "rate_limit") {
				t.Errorf("rate_limit.by=%q açılışı engelledi:\n%v", by, err)
			} else {
				t.Fatalf("fixture beklenmedik şekilde geçersiz:\n%v", err)
			}
		}
	}
}
