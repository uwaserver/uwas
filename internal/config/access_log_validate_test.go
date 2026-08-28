package config

import (
	"strings"
	"testing"
)

// access_log.format was ignored, so a typo produced no error and no change.
// Now that it is read, it is caught at load.
func TestValidateAccessLogFormat(t *testing.T) {
	cases := []struct {
		format  string
		wantErr bool
	}{
		{"", false},
		{"clf", false},
		{"json", false},
		{"JSON", false},
		{"custom", false},
		{"jsonn", true},
		{"saçmalık", true},
	}

	for _, c := range cases {
		cfg := &Config{Domains: []Domain{{
			Host:      "log.test",
			Type:      "static",
			Root:      "/srv/www",
			SSL:       SSLConfig{Mode: "off"},
			AccessLog: AccessLogConfig{Path: "/var/log/uwas/a.log", Format: c.format},
		}}}

		err := Validate(cfg)
		hasFormatErr := err != nil && strings.Contains(err.Error(), "access_log.format")
		if hasFormatErr != c.wantErr {
			t.Errorf("format=%q: hata=%v, beklenen=%v (%v)", c.format, hasFormatErr, c.wantErr, err)
		}
	}
}

func TestValidateAccessLogBufferSize(t *testing.T) {
	cfg := &Config{Domains: []Domain{{
		Host:      "log.test",
		Type:      "static",
		Root:      "/srv/www",
		SSL:       SSLConfig{Mode: "off"},
		AccessLog: AccessLogConfig{Path: "/var/log/uwas/a.log", BufferSize: -1},
	}}}

	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "access_log.buffer_size") {
		t.Errorf("negatif buffer_size kabul edildi: %v", err)
	}
}
