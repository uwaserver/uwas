package config

import (
	"strings"
	"testing"
)

// security.rate_limit.by was ignored, so a typo produced no error and silent
// per-IP keying. Now that it is read, it is caught at load.
func TestValidateRateLimitBy(t *testing.T) {
	cases := []struct {
		by      string
		wantErr bool
	}{
		{"", false},
		{"ip", false},
		{"IP", false},
		{"header:X-API-Key", false},
		{"header:X-Forwarded-For", false},
		{"header:", true},
		{"header", true},
		{"cookie:sid", true},
		{"saçmalık", true},
	}

	for _, c := range cases {
		cfg := &Config{Domains: []Domain{{
			Host:     "rl.test",
			Type:     "static",
			Root:     "/srv/www",
			SSL:      SSLConfig{Mode: "off"},
			Security: SecurityConfig{RateLimit: RateLimitConfig{Requests: 10, By: c.by}},
		}}}

		err := Validate(cfg)
		hasErr := err != nil && strings.Contains(err.Error(), "security.rate_limit.by")
		if hasErr != c.wantErr {
			t.Errorf("by=%q: hata=%v, beklenen=%v (%v)", c.by, hasErr, c.wantErr, err)
		}
	}
}
