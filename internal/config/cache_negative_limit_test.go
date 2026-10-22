package config

import (
	"strings"
	"testing"
)

// F24800: defaults replace only 0, so a negative cache size used to pass
// Validate. The disk cache reads a limit <= 0 as "unbounded" and the memory
// cache rejects every entry.
func TestValidateRejectsNegativeCacheSizes(t *testing.T) {
	mk := func(mem, disk ByteSize) *Config {
		return &Config{Global: GlobalConfig{
			LogLevel: "info", LogFormat: "json",
			Admin:   AdminConfig{Listen: "127.0.0.1:9443"},
			WebRoot: "/var/www",
			Cache:   CacheConfig{Enabled: true, MemoryLimit: mem, DiskLimit: disk},
		}}
	}
	cases := []struct {
		name      string
		mem, disk ByteSize
		want      string // "" = accepted
	}{
		{"positive", 64 * MB, 1 * GB, ""},
		{"zero defaults", 0, 0, ""},
		{"negative disk", 64 * MB, -1, "global.cache.disk_limit"},
		{"negative memory", -1, 1 * GB, "global.cache.memory_limit"},
		{"both negative", -5, -5, "global.cache.memory_limit"},
	}
	for _, c := range cases {
		err := ValidateWithDefaults(mk(c.mem, c.disk))
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want containing %q", c.name, err, c.want)
		}
	}
}
