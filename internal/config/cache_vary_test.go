package config

import "testing"

// vary_by_query is a pointer so an absent setting is distinguishable from an
// explicit false. The key has always included the query and the field was
// never read, so honouring a zero value literally would make /search?q=cats
// and /search?q=dogs share one entry on every deployment that never set it.
func TestQueryVaries(t *testing.T) {
	cases := []struct {
		ad   string
		in   *bool
		want bool
	}{
		{"ayarsız (yok) -> varyasyon sürer", nil, true},
		{"açıkça true", BoolPtr(true), true},
		{"açıkça false -> çöker", BoolPtr(false), false},
	}
	for _, c := range cases {
		t.Run(c.ad, func(t *testing.T) {
			if got := (CacheConfig{VaryByQuery: c.in}).QueryVaries(); got != c.want {
				t.Errorf("QueryVaries() = %v, want %v", got, c.want)
			}
		})
	}
}
