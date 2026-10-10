package autoblock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A persisted block whose kernel rule was installed but which load() skips —
// it expired while the process was down, or the IP has been whitelisted since —
// must still get its firewall rule removed. Otherwise nothing tracks the rule
// any more and the address stays kernel-banned forever.
func TestLoadSkippedBlockStillRemovesFirewallRule(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		entry     Entry
		whitelist []string
		want      int
	}{
		{"expired while down", Entry{IP: "203.0.113.5", ExpiresAt: now.Add(-time.Hour), Firewall: true}, nil, 1},
		{"whitelisted since", Entry{IP: "203.0.113.5", ExpiresAt: now.Add(time.Hour), Firewall: true}, []string{"203.0.113.5"}, 1},
		{"rule never confirmed", Entry{IP: "203.0.113.5", ExpiresAt: now.Add(-time.Hour)}, nil, 0},
		{"dry run", Entry{IP: "203.0.113.5", ExpiresAt: now.Add(-time.Hour), Firewall: true, DryRun: true}, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "autoblock.json")
			tc.entry.Reason, tc.entry.Level, tc.entry.BlockedAt = ReasonConnFlood, 1, now.Add(-2*time.Hour)
			data, err := json.Marshal(persisted{Blocks: []Entry{tc.entry}, Saved: now})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			b := testBlocker(t, func(c *Config) {
				c.FirewallSync = true
				c.PersistPath = path
				c.Whitelist = tc.whitelist
			})
			removed := 0
			b.SetFirewall(func(string, string) error { return nil },
				func(string) error { removed++; return nil })
			drain := func() {
				for {
					select {
					case op := <-b.fwQueue:
						b.runFirewallOp(op)
					default:
						return
					}
				}
			}
			for i := 0; i < 2; i++ { // two ticks: delivered once, not repeated
				b.expire()
				drain()
			}
			if removed != tc.want {
				t.Fatalf("firewall rule removed %d times, want %d", removed, tc.want)
			}
		})
	}
}
