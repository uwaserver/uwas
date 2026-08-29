package domain

import (
	"encoding/json"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// applyCacheDefault mirrors the rule Add applies, so the decision can be
// tested without standing up the whole admin server. Keep the two in step.
func applyCacheDefault(t *testing.T, body string) config.Domain {
	t.Helper()
	var d config.Domain
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(body), &raw)
	if !rawHas(raw, "cache") && (d.Type == "" || d.Type == string(config.DomainTypeStatic)) {
		d.Cache.Enabled = true
	}
	return d
}

func TestNewDomainCacheDefault(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"static with no cache key", `{"host":"a.test","type":"static"}`, true},
		{"type omitted defaults to static", `{"host":"a.test"}`, true},
		{"php is left alone", `{"host":"a.test","type":"php"}`, false},
		{"proxy is left alone", `{"host":"a.test","type":"proxy"}`, false},
		{"redirect is left alone", `{"host":"a.test","type":"redirect"}`, false},
		// Presence of the key is the signal: an explicit disable must survive.
		{"explicit disable wins", `{"host":"a.test","type":"static","cache":{"enabled":false}}`, false},
		{"explicit enable stays enabled", `{"host":"a.test","type":"static","cache":{"enabled":true}}`, true},
		{"explicit empty cache object", `{"host":"a.test","type":"static","cache":{}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := applyCacheDefault(t, tt.body)
			if d.Cache.Enabled != tt.want {
				t.Errorf("cache.enabled = %v, want %v", d.Cache.Enabled, tt.want)
			}
		})
	}
}

// TestNewDomainCacheDefaultLeavesTTL checks the default does not invent a TTL:
// zero means "fall back to global.cache.default_ttl" at serve time.
func TestNewDomainCacheDefaultLeavesTTL(t *testing.T) {
	d := applyCacheDefault(t, `{"host":"a.test","type":"static"}`)
	if d.Cache.TTL != 0 {
		t.Errorf("cache.ttl = %d, want 0 so the global default applies", d.Cache.TTL)
	}
}
