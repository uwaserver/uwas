package config

import (
	"strings"
	"testing"
	"time"
)

// Defaults only replace 0, so negative global timeouts / max_connections used
// to pass validation and reach net/http, where a timeout <= 0 disables it (F1390).
func TestValidateRejectsNegativeGlobalLimits(t *testing.T) {
	mk := func() *Config {
		return &Config{Global: GlobalConfig{
			LogLevel: "info", LogFormat: "json",
			Admin:   AdminConfig{Listen: "127.0.0.1:9443"},
			WebRoot: "/var/www",
		}}
	}
	if err := ValidateWithDefaults(mk()); err != nil {
		t.Fatalf("defaults must stay valid: %v", err)
	}
	cases := map[string]func(*Config){
		"global.timeouts.read":           func(c *Config) { c.Global.Timeouts.Read.Duration = -time.Second },
		"global.timeouts.read_header":    func(c *Config) { c.Global.Timeouts.ReadHeader.Duration = -time.Second },
		"global.timeouts.write":          func(c *Config) { c.Global.Timeouts.Write.Duration = -time.Second },
		"global.timeouts.idle":           func(c *Config) { c.Global.Timeouts.Idle.Duration = -time.Second },
		"global.timeouts.shutdown_grace": func(c *Config) { c.Global.Timeouts.ShutdownGrace.Duration = -time.Second },
		"global.max_connections":         func(c *Config) { c.Global.MaxConnections = -1 },
	}
	for want, mut := range cases {
		c := mk()
		mut(c)
		err := ValidateWithDefaults(c)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err=%v, want rejection naming the field", want, err)
		}
	}
	// Positive values and an explicit zero (= default / unlimited) stay valid.
	c := mk()
	c.Global.Timeouts.Read.Duration = 5 * time.Second
	c.Global.Timeouts.ShutdownGrace.Duration = time.Minute
	c.Global.MaxConnections = 100
	if err := ValidateWithDefaults(c); err != nil {
		t.Errorf("positive values rejected: %v", err)
	}
}
