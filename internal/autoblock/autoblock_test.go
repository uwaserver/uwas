package autoblock

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

func testLogger() *logger.Logger { return logger.New("error", "text") }

func testBlocker(t *testing.T, mutate func(*Config)) *Blocker {
	t.Helper()
	cfg := Config{
		Enabled:        true,
		Window:         time.Minute,
		MaxConnections: 5,
		MaxAborts:      3,
		MaxConcurrent:  4,
		MaxWAFHits:     2,
		MaxRateHits:    3,
		MaxNotFound:    4,
		BlockDuration:  time.Hour,
		FeedRateHits:   true,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg, testLogger())
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return a
}

func TestConnFloodBlocks(t *testing.T) {
	// Concurrency is off here so the count of new connections is what trips,
	// not the simultaneous-connection gauge.
	b := testBlocker(t, func(c *Config) { c.MaxConcurrent = 0 })
	a := mustAddr(t, "203.0.113.7")

	for i := 0; i < 5; i++ {
		if !b.ConnOpened(a) {
			t.Fatalf("connection %d refused below threshold", i)
		}
	}
	if b.ConnOpened(a) {
		t.Fatal("connection 6 should have tripped max_connections")
	}
	if !b.Blocked(a) {
		t.Fatal("IP should be blocked after tripping the connection threshold")
	}
	if b.ConnOpened(a) {
		t.Fatal("blocked IP must stay refused")
	}
}

// The attack in the field opened TCP connections and hung up before sending a
// ClientHello, producing one "TLS handshake error ... EOF" per connection. No
// HTTP request ever existed, so only the aborted-connection counter can see it.
func TestTLSAbortFloodBlocks(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.MaxConnections = 1000 })
	a := mustAddr(t, "35.207.219.84")

	for i := 0; i < 3; i++ {
		b.ConnOpened(a)
		b.ConnClosed(a, true)
	}
	if b.Blocked(a) {
		t.Fatal("blocked at the threshold rather than past it")
	}
	b.ConnOpened(a)
	b.ConnClosed(a, true)
	if !b.Blocked(a) {
		t.Fatal("4 aborted connections should trip max_aborts=3")
	}
}

// A connection that sent bytes is a real client, however short-lived.
func TestCompletedConnectionsNeverCountAsAborts(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.MaxConnections = 1000 })
	a := mustAddr(t, "198.51.100.9")

	for i := 0; i < 50; i++ {
		b.ConnOpened(a)
		b.ConnClosed(a, false)
	}
	if b.Blocked(a) {
		t.Fatal("normal traffic must not be blocked")
	}
}

func TestConcurrentLimit(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.MaxConnections = 1000 })
	a := mustAddr(t, "198.51.100.20")

	for i := 0; i < 4; i++ {
		if !b.ConnOpened(a) {
			t.Fatalf("connection %d refused below concurrency limit", i)
		}
	}
	if b.ConnOpened(a) {
		t.Fatal("5th simultaneous connection should trip max_concurrent=4")
	}
}

// Closing connections must return the concurrency slot, or a busy but
// legitimate client is blocked the moment its lifetime total passes the gauge.
func TestConcurrentReleasedOnClose(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.MaxConnections = 1000 })
	a := mustAddr(t, "198.51.100.21")

	for i := 0; i < 20; i++ {
		if !b.ConnOpened(a) {
			t.Fatalf("connection %d refused: concurrency slot leaked", i)
		}
		b.ConnClosed(a, false)
	}
	if b.Blocked(a) {
		t.Fatal("sequential connections must not trip the concurrency gauge")
	}
}

func TestHTTPSignalsBlock(t *testing.T) {
	for _, tc := range []struct {
		reason string
		hits   int
		ip     string
	}{
		{ReasonWAF, 3, "203.0.113.31"},
		{ReasonRate, 4, "203.0.113.32"},
		{ReasonNotFound, 5, "203.0.113.33"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			b := testBlocker(t, nil)
			for i := 0; i < tc.hits; i++ {
				b.RecordHTTP(tc.ip+":54321", tc.reason)
			}
			if !b.BlockedAddr(tc.ip + ":54321") {
				t.Fatalf("%s: %d hits should have tripped the threshold", tc.reason, tc.hits)
			}
		})
	}
}

func TestWhitelistedAndReservedAddressesAreNeverBlocked(t *testing.T) {
	b := testBlocker(t, func(c *Config) {
		c.Whitelist = []string{"203.0.113.0/24", "198.51.100.77"}
	})

	for _, ip := range []string{
		"127.0.0.1",       // loopback: blocking it kills the health probe
		"10.1.2.3",        // private
		"192.168.1.50",    // private
		"169.254.169.254", // link-local
		"203.0.113.99",    // configured CIDR
		"198.51.100.77",   // configured single address
	} {
		a := mustAddr(t, ip)
		for i := 0; i < 200; i++ {
			b.ConnOpened(a)
			b.ConnClosed(a, true)
		}
		if b.Blocked(a) {
			t.Fatalf("%s must never be blocked", ip)
		}
		if err := b.Block(ip, ReasonManual, time.Hour); err == nil {
			t.Fatalf("manual Block(%s) should be refused", ip)
		}
	}
}

// A CDN edge that trips max_connections before cloudflare.ip_ranges is synced
// must come back online the moment those ranges land on reload — otherwise the
// whitelist refresh does nothing useful and the site stays dark.
func TestSetWhitelistLiftsActiveBlocksThatAreNowSafe(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.MaxConcurrent = 0; c.MaxConnections = 2 })
	a := mustAddr(t, "104.16.0.1") // looks like a Cloudflare edge

	if !b.ConnOpened(a) || !b.ConnOpened(a) {
		t.Fatal("first two connections should pass")
	}
	if b.ConnOpened(a) {
		t.Fatal("third connection should trip conn_flood")
	}
	if !b.Blocked(a) {
		t.Fatal("edge should be blocked before the whitelist refresh")
	}

	b.SetWhitelist([]string{"104.16.0.0/13"})

	if b.Blocked(a) {
		t.Fatal("Blocked must return false once the edge is Safe")
	}
	if !b.ConnOpened(a) {
		t.Fatal("ConnOpened must serve traffic again after SetWhitelist")
	}
	for _, e := range b.List() {
		if e.IP == a.String() {
			t.Fatal("active block list must no longer contain the now-safe edge")
		}
	}
}

func TestEscalation(t *testing.T) {
	b := testBlocker(t, func(c *Config) {
		c.Escalate = true
		c.BlockDuration = time.Minute
		c.MaxBlockDuration = time.Hour
	})

	if got := b.escalated(1); got != time.Minute {
		t.Fatalf("level 1 = %v, want 1m", got)
	}
	if got := b.escalated(2); got != 4*time.Minute {
		t.Fatalf("level 2 = %v, want 4m", got)
	}
	if got := b.escalated(3); got != 16*time.Minute {
		t.Fatalf("level 3 = %v, want 16m", got)
	}
	// Capped, not unbounded: an escalation that runs away would blackhole an
	// IP for years off a single bad window.
	if got := b.escalated(9); got != time.Hour {
		t.Fatalf("level 9 = %v, want the 1h cap", got)
	}
}

func TestEscalationOffKeepsBaseDuration(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.Escalate = false; c.BlockDuration = time.Minute })
	if got := b.escalated(5); got != time.Minute {
		t.Fatalf("escalate=false level 5 = %v, want 1m", got)
	}
}

func TestDryRunDetectsButDoesNotEnforce(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.DryRun = true })
	a := mustAddr(t, "203.0.113.44")

	for i := 0; i < 20; i++ {
		b.ConnOpened(a)
	}
	if b.Blocked(a) {
		t.Fatal("dry run must not enforce the block")
	}
	if len(b.List()) == 0 {
		t.Fatal("dry run must still record the detection")
	}
}

func TestUnblockClearsEscalationHistory(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.Escalate = true })
	if err := b.Block("203.0.113.50", ReasonManual, time.Hour); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := b.Unblock("203.0.113.50"); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if b.BlockedAddr("203.0.113.50") {
		t.Fatal("still blocked after Unblock")
	}
	// An operator who cleared an IP by hand should not see it return at
	// level 2 on its next offence.
	if err := b.Block("203.0.113.50", ReasonManual, time.Hour); err != nil {
		t.Fatalf("re-block: %v", err)
	}
	entries := b.List()
	if len(entries) != 1 || entries[0].Level != 1 {
		t.Fatalf("expected level 1 after an operator unblock, got %+v", entries)
	}
}

func TestUnblockUnknownIPReportsError(t *testing.T) {
	b := testBlocker(t, nil)
	if err := b.Unblock("203.0.113.51"); err == nil {
		t.Fatal("expected an error unblocking an IP that is not blocked")
	}
	if err := b.Unblock("not-an-ip"); err == nil {
		t.Fatal("expected an error for a malformed address")
	}
}

func TestExpiryLiftsBlockAndReleasesFirewallRule(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.BlockDuration = time.Millisecond })

	var mu sync.Mutex
	var unblocked []string
	b.SetFirewall(
		func(ip, comment string) error { return nil },
		func(ip string) error { mu.Lock(); unblocked = append(unblocked, ip); mu.Unlock(); return nil },
	)
	b.SetFirewallSync(true)

	ctx, cancel := context.WithCancel(context.Background())
	go b.firewallWorker(ctx)
	defer cancel()

	if err := b.Block("203.0.113.60", ReasonManual, 2*time.Millisecond); err != nil {
		t.Fatalf("block: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	b.expire()

	if b.BlockedAddr("203.0.113.60") {
		t.Fatal("block should have expired")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(unblocked)
		mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expiry should have released the firewall rule")
}

func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "autoblock.json")
	cfg := Config{Enabled: true, BlockDuration: time.Hour, PersistPath: path}

	b := New(cfg, testLogger())
	if err := b.Block("203.0.113.70", ReasonConnFlood, time.Hour); err != nil {
		t.Fatalf("block: %v", err)
	}
	b.save()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	// An attack that survives a restart must not get a clean slate.
	restored := New(cfg, testLogger())
	if !restored.BlockedAddr("203.0.113.70") {
		t.Fatal("block did not survive the restart")
	}
}

func TestExpiredBlocksAreNotRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autoblock.json")
	cfg := Config{Enabled: true, BlockDuration: time.Hour, PersistPath: path}

	b := New(cfg, testLogger())
	if err := b.Block("203.0.113.71", ReasonConnFlood, time.Millisecond); err != nil {
		t.Fatalf("block: %v", err)
	}
	b.save()
	time.Sleep(10 * time.Millisecond)

	if New(cfg, testLogger()).BlockedAddr("203.0.113.71") {
		t.Fatal("an expired block must not come back on restart")
	}
}

func TestDisabledBlockerIsInert(t *testing.T) {
	b := New(Config{Enabled: false}, testLogger())
	a := mustAddr(t, "203.0.113.80")
	for i := 0; i < 10000; i++ {
		if !b.ConnOpened(a) {
			t.Fatal("a disabled blocker must accept everything")
		}
		b.ConnClosed(a, true)
	}
	if b.Blocked(a) {
		t.Fatal("a disabled blocker must block nothing")
	}
}

func TestNilBlockerIsSafe(t *testing.T) {
	var b *Blocker
	if !b.ConnOpened(mustAddr(t, "203.0.113.81")) {
		t.Fatal("nil blocker should accept")
	}
	b.ConnClosed(mustAddr(t, "203.0.113.81"), true)
	b.RecordHTTP("203.0.113.81:1", ReasonWAF)
	if b.Enabled() || b.Blocked(mustAddr(t, "203.0.113.81")) {
		t.Fatal("nil blocker should report disabled and block nothing")
	}
	if st := b.Stats(); st["enabled"] != false {
		t.Fatal("nil blocker stats should report disabled")
	}
}

func TestWindowResetsCounters(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.Window = 20 * time.Millisecond })
	a := mustAddr(t, "203.0.113.90")

	for i := 0; i < 5; i++ {
		b.ConnOpened(a)
		b.ConnClosed(a, false)
	}
	time.Sleep(40 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if !b.ConnOpened(a) {
			t.Fatalf("connection %d refused: window did not reset", i)
		}
		b.ConnClosed(a, false)
	}
}

func TestParseAddrForms(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1.2.3.4:5678", "1.2.3.4"},
		{"1.2.3.4", "1.2.3.4"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"2001:db8::1", "2001:db8::1"},
		{"::ffff:1.2.3.4", "1.2.3.4"}, // 4-in-6 must normalise, or it evades a v4 block
	} {
		got, ok := ParseAddr(tc.in)
		if !ok || got.String() != tc.want {
			t.Errorf("ParseAddr(%q) = %v/%v, want %s", tc.in, got, ok, tc.want)
		}
	}
	for _, bad := range []string{"", "nope", "example.com"} {
		if _, ok := ParseAddr(bad); ok {
			t.Errorf("ParseAddr(%q) should fail", bad)
		}
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	c := Config{}
	c.Normalize()
	// Normalize fills the time/count defaults it owns. MaxConnections is
	// deliberately NOT defaulted here — 0 means "disabled", and the config
	// layer supplies the default when the field is absent.
	if c.Window <= 0 || c.MaxAborts <= 0 || c.BlockDuration <= 0 {
		t.Fatalf("Normalize left a zero threshold: %+v", c)
	}
	if c.MaxConnections != 0 {
		t.Fatalf("Normalize must leave MaxConnections at 0 (disabled) when unset, got %d", c.MaxConnections)
	}
	// A max shorter than the base would make escalation shrink the block.
	c2 := Config{BlockDuration: time.Hour, MaxBlockDuration: time.Minute}
	c2.Normalize()
	if c2.MaxBlockDuration < c2.BlockDuration {
		t.Fatalf("MaxBlockDuration %v < BlockDuration %v", c2.MaxBlockDuration, c2.BlockDuration)
	}
}

func TestStatsReportsActiveBlocks(t *testing.T) {
	b := testBlocker(t, nil)
	_ = b.Block("203.0.113.91", ReasonConnFlood, time.Hour)
	_ = b.Block("203.0.113.92", ReasonWAF, time.Hour)

	st := b.Stats()
	if st["active_blocks"] != 2 {
		t.Fatalf("active_blocks = %v, want 2", st["active_blocks"])
	}
	byReason, _ := st["by_reason"].(map[string]int)
	if byReason[ReasonConnFlood] != 1 || byReason[ReasonWAF] != 1 {
		t.Fatalf("by_reason = %v", byReason)
	}
}

func TestConcurrentAccessIsRaceFree(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.MaxConnections = 100000; c.MaxConcurrent = 0 })
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := netip.AddrFrom4([4]byte{203, 0, 113, byte(100 + i%8)})
			for j := 0; j < 500; j++ {
				b.ConnOpened(a)
				b.ConnClosed(a, j%3 == 0)
				b.RecordHTTP(a.String()+":1234", ReasonWAF)
				b.Blocked(a)
			}
		}(i)
	}
	wg.Wait()
}

// A refused connection is closed by the listener without being wrapped, so
// ConnClosed never runs for it. If ConnOpened did not hand the concurrency
// slot back, every refusal would leak one: the gauge would never return to
// zero and the IP's counters would stay permanently ineligible for GC.
func TestRefusedConnectionReleasesItsConcurrencySlot(t *testing.T) {
	b := testBlocker(t, func(c *Config) {
		c.MaxConnections = 3
		c.MaxConcurrent = 100
		c.BlockDuration = time.Millisecond
	})
	a := mustAddr(t, "203.0.113.120")

	for i := 0; i < 4; i++ {
		if b.ConnOpened(a) {
			b.ConnClosed(a, false)
		}
	}
	if !b.Blocked(a) {
		t.Fatal("expected the connection threshold to trip")
	}

	// Let the block lapse, then confirm the gauge came back to zero: a leaked
	// slot would leave a stale count behind and keep the track alive forever.
	time.Sleep(10 * time.Millisecond)
	b.expire()

	sh := b.shardFor(a)
	sh.mu.Lock()
	tr, ok := sh.tracks[a]
	var concurrent int
	if ok {
		concurrent = tr.concurrent
	}
	sh.mu.Unlock()

	if concurrent != 0 {
		t.Fatalf("concurrent = %d after every connection was accounted for, want 0", concurrent)
	}

	// And with the gauge clean, the idle counters must be collectable.
	b.cfg.Window = time.Nanosecond
	time.Sleep(2 * time.Millisecond)
	b.gcTracks()

	sh.mu.Lock()
	_, stillTracked := sh.tracks[a]
	sh.mu.Unlock()
	if stillTracked {
		t.Fatal("idle counters were not collected: a leaked slot pins them forever")
	}
}

// max_connections = 0 disables the connection-count check (operators need to
// turn it off — it false-positives on connection-heavy / NAT'd clients —
// without losing the abort detector).
func TestMaxConnectionsZeroDisables(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.MaxConnections = 0; c.MaxConcurrent = 0; c.MaxAborts = 1000 })
	a := mustAddr(t, "203.0.113.130")
	for i := 0; i < 5000; i++ {
		if !b.ConnOpened(a) {
			t.Fatalf("connection %d refused although max_connections is disabled (0)", i)
		}
		b.ConnClosed(a, false)
	}
	if b.Blocked(a) {
		t.Fatal("max_connections=0 must not block on connection count")
	}
}

// FeedRateHits=false keeps rate limiting a soft throttle: repeated "rate"
// signals never accrue toward a block.
func TestFeedRateHitsFalseIgnoresRate(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.FeedRateHits = false; c.MaxRateHits = 3 })
	for i := 0; i < 50; i++ {
		b.RecordHTTP("203.0.113.131:5555", ReasonRate)
	}
	if b.BlockedAddr("203.0.113.131:5555") {
		t.Fatal("FeedRateHits=false must not let rate hits escalate to a block")
	}
}

// With FeedRateHits on (default), rate hits still block past the threshold.
func TestFeedRateHitsTrueStillBlocks(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.FeedRateHits = true; c.MaxRateHits = 3 })
	for i := 0; i < 5; i++ {
		b.RecordHTTP("203.0.113.132:5555", ReasonRate)
	}
	if !b.BlockedAddr("203.0.113.132:5555") {
		t.Fatal("FeedRateHits=true must block past max_rate_hits")
	}
}
