package config

import (
	"strings"
	"testing"
)

// access_log.format was ignored until now, so a config already carrying an
// unrecognised value has been running fine. Rejecting it at load would turn
// an upgrade into a server that will not start: Load() calls Validate and
// serve refuses to run on the error. The writer falls back to clf and the
// server warns once at startup.
func TestUnknownAccessLogFormatDoesNotBlockStartup(t *testing.T) {
	for _, format := range []string{"combined", "json_lines", "saçmalık", ""} {
		cfg := &Config{
			Global: GlobalConfig{LogLevel: "info", LogFormat: "text"},
			Domains: []Domain{{
				Host:      "log.test",
				Type:      "static",
				Root:      "/srv/www",
				SSL:       SSLConfig{Mode: "off"},
				AccessLog: AccessLogConfig{Path: "/var/log/uwas/a.log", Format: format, BufferSize: -1},
			}},
		}
		if err := Validate(cfg); err != nil {
			if strings.Contains(err.Error(), "access_log") {
				t.Errorf("access_log.format=%q açılışı engelledi:\n%v", format, err)
			} else {
				t.Fatalf("fixture beklenmedik şekilde geçersiz:\n%v", err)
			}
		}
	}
}
