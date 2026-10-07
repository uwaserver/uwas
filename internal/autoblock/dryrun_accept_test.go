package autoblock

import (
	"testing"
)

// dry_run exists so an operator can calibrate thresholds against real traffic
// without taking visitors offline (Config.DryRun, config.AutoBlockConfig.DryRun).
// ConnOpened is what guardListener.Accept acts on: a false return makes the
// listener close that socket. So under dry_run the detector must still trip and
// record what it saw, but must never refuse the connection — the refusal is the
// enforcement, independently of whether the block reaches the enforced snapshot.
func TestDryRunNeverRefusesConnectionAtAccept(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name:   "connection flood threshold",
			mutate: func(c *Config) { c.MaxConcurrent = 0; c.MaxConnections = 5 },
		},
		{
			name:   "concurrent connection threshold",
			mutate: func(c *Config) { c.MaxConnections = 0; c.MaxConcurrent = 3 },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := testBlocker(t, func(c *Config) {
				c.DryRun = true
				tc.mutate(c)
			})
			a := mustAddr(t, "203.0.113.90")

			for i := 0; i < 20; i++ {
				if !b.ConnOpened(a) {
					t.Fatalf("dry_run refused connection %d at Accept; the listener "+
						"closes that socket, which is enforcement dry_run must not do",
						i+1)
				}
			}

			// Detection still happens: dry_run is "do not enforce", not "off".
			if len(b.List()) == 0 {
				t.Fatal("dry_run must still record the detection it would have blocked")
			}
			if b.Blocked(a) {
				t.Fatal("dry_run must keep the IP out of the enforced snapshot")
			}
		})
	}
}

// Control: the same input sequence must still be refused with enforcement on,
// so the dry-run assertion above cannot pass on a blocker that never refuses.
func TestDryRunControlEnforcementStillRefuses(t *testing.T) {
	b := testBlocker(t, func(c *Config) {
		c.DryRun = false
		c.MaxConcurrent = 0
		c.MaxConnections = 5
	})
	a := mustAddr(t, "203.0.113.91")

	refused := false
	for i := 0; i < 20; i++ {
		if !b.ConnOpened(a) {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("control: enforcement must still refuse past max_connections")
	}
	if !b.Blocked(a) {
		t.Fatal("control: enforcement must place the IP in the enforced snapshot")
	}
}

// Boundary: a refused connection under enforcement never reaches ConnClosed, so
// ConnOpened hands its concurrency slot back explicitly. Under dry_run the
// socket IS served, so the slot must be left for guardConn.Close → ConnClosed.
// Releasing on the dry-run path as well would under-count and let the gauge
// drift below the real number of open connections.
func TestDryRunKeepsConcurrencySlotForServedConnection(t *testing.T) {
	b := testBlocker(t, func(c *Config) {
		c.DryRun = true
		c.MaxConnections = 0
		c.MaxConcurrent = 3
	})
	a := mustAddr(t, "203.0.113.92")

	for i := 0; i < 6; i++ {
		if !b.ConnOpened(a) {
			t.Fatalf("dry_run refused connection %d", i+1)
		}
	}
	// Three served connections beyond MaxConcurrent, none closed yet: the
	// gauge must still be holding all six slots.
	sh := b.shardFor(a)
	sh.mu.Lock()
	held := sh.tracks[a].concurrent
	sh.mu.Unlock()
	if held != 6 {
		t.Fatalf("concurrency gauge = %d, want 6: a served dry-run connection must "+
			"keep its slot for ConnClosed to release", held)
	}

	// Closing them hands every slot back and drops the gauge to zero.
	for i := 0; i < 6; i++ {
		b.ConnClosed(a, false)
	}
	sh.mu.Lock()
	held = sh.tracks[a].concurrent
	sh.mu.Unlock()
	if held != 0 {
		t.Fatalf("concurrency gauge after closing = %d, want 0", held)
	}
}

// Enforcement must still release the slot it could not hand to ConnClosed,
// otherwise the gauge leaks one per refused connection and the IP becomes
// permanently ineligible for GC. This is the branch the dry-run fix must not
// have broken.
func TestEnforcementReleasesSlotOnRefusal(t *testing.T) {
	b := testBlocker(t, func(c *Config) {
		c.DryRun = false
		c.MaxConnections = 0
		c.MaxConcurrent = 2
	})
	a := mustAddr(t, "203.0.113.93")

	// Two served, then refusals. Refused sockets are never wrapped, so no
	// ConnClosed follows for them.
	b.ConnOpened(a)
	b.ConnOpened(a)
	for i := 0; i < 5; i++ {
		if b.ConnOpened(a) {
			t.Fatalf("connection %d should have been refused past max_concurrent", i+3)
		}
	}

	sh := b.shardFor(a)
	sh.mu.Lock()
	held := sh.tracks[a].concurrent
	sh.mu.Unlock()
	if held != 2 {
		t.Fatalf("concurrency gauge = %d, want 2: a refused connection must hand its "+
			"slot back immediately", held)
	}

	// Closing the two that were actually served returns the gauge to zero.
	b.ConnClosed(a, false)
	b.ConnClosed(a, false)
	sh.mu.Lock()
	held = sh.tracks[a].concurrent
	sh.mu.Unlock()
	if held != 0 {
		t.Fatalf("concurrency gauge after closing served connections = %d, want 0", held)
	}
}
