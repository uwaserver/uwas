// Package autoblock detects abusive source IPs and blocks them, first in
// memory at the accept path and then — optionally — in the host firewall.
//
// It exists because the HTTP-layer defences (WAF, rate limit, IP ACL) only
// ever see a request that finished a TLS handshake. A flood that opens TCP
// connections and drops them mid-handshake never reaches them: the server
// logs "http: TLS handshake error ... EOF" per connection and burns its
// accept loop, file descriptors and CPU on traffic no middleware can refuse.
// So the counters here are fed from two places — the listener wrapper, which
// sees every connection, and the HTTP guards, which see the requests that get
// through — and the block decision is enforced at Accept, before TLS.
package autoblock

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// shardCount partitions the per-IP counters. A flood is many source IPs
// hammering at once, so a single mutex over one map would serialise the
// accept path — the exact thing this package is meant to keep cheap.
const shardCount = 256

// Reason labels why an IP was blocked. These are stable strings: they appear
// in the persisted state file, the admin API and the ufw rule comment.
const (
	ReasonConnFlood  = "conn_flood" // too many new connections in the window
	ReasonTLSAbort   = "tls_abort"  // connections closed without sending a byte
	ReasonConcurrent = "concurrent" // too many simultaneous open connections
	ReasonWAF        = "waf"        // repeated WAF rule hits
	ReasonRate       = "rate"       // repeated rate-limit rejections
	ReasonBot        = "bot"        // repeated bot-guard rejections
	ReasonNotFound   = "notfound"   // 404 sweep (vulnerability scanning)
	ReasonManual     = "manual"     // operator action via API/CLI
)

// Config is the operator-facing policy. Zero values are filled by Normalize,
// so an empty Config with Enabled=true is a usable default policy.
type Config struct {
	Enabled bool

	// Window is the counting period. All Max* thresholds are "per source IP,
	// per window".
	Window time.Duration

	MaxConnections int // new TCP connections
	MaxAborts      int // connections closed having sent nothing (handshake floods)
	MaxConcurrent  int // simultaneous open connections; 0 disables
	MaxWAFHits     int
	MaxRateHits    int
	MaxNotFound    int

	// BlockDuration is the first offence. Repeat offenders escalate 4x per
	// level up to MaxBlockDuration when Escalate is set.
	BlockDuration    time.Duration
	MaxBlockDuration time.Duration
	Escalate         bool

	// DryRun detects and logs but never actually blocks. Use it to calibrate
	// thresholds against real traffic before switching enforcement on.
	DryRun bool

	// Whitelist holds CIDRs or bare IPs that are never blocked, on top of the
	// always-safe set (loopback, private, link-local) applied unconditionally.
	Whitelist []string

	// FirewallSync pushes blocks down to ufw. Without it a blocked IP still
	// completes a TCP handshake with us before being dropped; with it the
	// kernel refuses the SYN and the process never wakes up at all.
	FirewallSync bool

	// PersistPath stores active blocks across restarts. An attack that
	// survives a restart should not get a clean slate.
	PersistPath string
}

// Normalize fills unset fields with defaults and clamps nonsense values.
func (c *Config) Normalize() {
	if c.Window <= 0 {
		c.Window = time.Minute
	}
	if c.MaxConnections <= 0 {
		c.MaxConnections = 600
	}
	if c.MaxAborts <= 0 {
		c.MaxAborts = 60
	}
	if c.MaxWAFHits <= 0 {
		c.MaxWAFHits = 15
	}
	if c.MaxRateHits <= 0 {
		c.MaxRateHits = 120
	}
	if c.MaxNotFound <= 0 {
		c.MaxNotFound = 200
	}
	if c.BlockDuration <= 0 {
		c.BlockDuration = 15 * time.Minute
	}
	if c.MaxBlockDuration <= 0 {
		c.MaxBlockDuration = 24 * time.Hour
	}
	if c.MaxBlockDuration < c.BlockDuration {
		c.MaxBlockDuration = c.BlockDuration
	}
}

// Entry is one blocked source IP.
type Entry struct {
	IP        string    `json:"ip"`
	Reason    string    `json:"reason"`
	Hits      int       `json:"hits"`  // signal count that tripped the threshold
	Level     int       `json:"level"` // escalation level, 1 = first offence
	BlockedAt time.Time `json:"blocked_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Firewall  bool      `json:"firewall"` // pushed to ufw
	DryRun    bool      `json:"dry_run,omitempty"`
}

// Expired reports whether the block has lapsed.
func (e *Entry) Expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt)
}

type shard struct {
	mu     sync.Mutex
	tracks map[netip.Addr]*track
}

// track holds one IP's counters for the current window.
type track struct {
	windowStart time.Time
	conns       int
	aborts      int
	waf         int
	rate        int
	notfound    int
	concurrent  int
	lastSeen    time.Time
}

// Blocker owns detection, the block list and firewall synchronisation.
type Blocker struct {
	cfg Config
	log *logger.Logger

	shards [shardCount]shard

	// blocked is the authority; view is a copy-on-write snapshot read by
	// Blocked() on every accepted connection. Blocks change rarely and are
	// read constantly, so readers must not touch a mutex the writer holds
	// while shelling out to ufw.
	mu      sync.Mutex
	blocked map[netip.Addr]*Entry
	view    atomic.Pointer[map[netip.Addr]struct{}]

	// history remembers escalation level per IP after a block expires, so a
	// returning offender is not treated as a first offence forever.
	history map[netip.Addr]int

	safeNets  atomic.Pointer[[]netip.Prefix]
	fwEnabled atomic.Bool

	// fwQueue serialises ufw calls off the request path. `ufw insert` takes
	// tens of milliseconds and holds its own lock; doing it inline would
	// stall the accept loop precisely when it is busiest.
	fwQueue  chan fwOp
	fwBlock  func(ip, comment string) error
	fwUnlock func(ip string) error

	saveOnce  chan struct{}
	blockedN  atomic.Int64
	detectedN atomic.Int64
}

type fwOp struct {
	ip     string
	remove bool
}

// New creates a Blocker. Call Start to run its background loops.
func New(cfg Config, log *logger.Logger) *Blocker {
	cfg.Normalize()
	b := &Blocker{
		cfg:      cfg,
		log:      log,
		blocked:  make(map[netip.Addr]*Entry),
		history:  make(map[netip.Addr]int),
		fwQueue:  make(chan fwOp, 1024),
		saveOnce: make(chan struct{}, 1),
	}
	for i := range b.shards {
		b.shards[i].tracks = make(map[netip.Addr]*track)
	}
	b.publish()
	b.SetWhitelist(cfg.Whitelist)
	b.fwEnabled.Store(cfg.FirewallSync)
	b.load()
	return b
}

// SetFirewall installs the block/unblock hooks used when FirewallSync is on.
// Kept as function fields rather than an import so this package stays
// testable without a real ufw and does not depend on internal/firewall.
func (b *Blocker) SetFirewall(block func(ip, comment string) error, unblock func(ip string) error) {
	b.fwBlock, b.fwUnlock = block, unblock
}

// SetFirewallSync toggles firewall synchronisation at runtime (config reload).
func (b *Blocker) SetFirewallSync(on bool) { b.fwEnabled.Store(on) }

// Enabled reports whether detection is active.
func (b *Blocker) Enabled() bool { return b != nil && b.cfg.Enabled }

// SetWhitelist replaces the never-block list. The always-safe ranges are
// re-added every time, so a reload cannot drop them.
//
// Callers should feed this the trusted_proxies and the Cloudflare edge ranges:
// behind a CDN every connection carries an edge IP, and blocking one of those
// takes the site off the internet for everyone that edge serves.
func (b *Blocker) SetWhitelist(entries []string) {
	nets := defaultSafeNets()
	for _, raw := range entries {
		if p, err := netip.ParsePrefix(raw); err == nil {
			nets = append(nets, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(raw); err == nil {
			nets = append(nets, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	b.safeNets.Store(&nets)
}

// defaultSafeNets are never blockable regardless of configuration: blocking
// them locks out the loopback health probe, the LAN, or the host itself.
func defaultSafeNets() []netip.Prefix {
	raw := []string{
		"127.0.0.0/8", "::1/128",
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"169.254.0.0/16", "fe80::/10", "fc00::/7",
		"0.0.0.0/32", "::/128",
	}
	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
		}
	}
	return out
}

// Safe reports whether an address is exempt from blocking.
func (b *Blocker) Safe(a netip.Addr) bool {
	nets := b.safeNets.Load()
	if nets == nil {
		return false
	}
	a = a.Unmap()
	for _, p := range *nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Blocked is the hot path: one map lookup on a snapshot, no locking.
func (b *Blocker) Blocked(a netip.Addr) bool {
	if b == nil || !b.cfg.Enabled {
		return false
	}
	v := b.view.Load()
	if v == nil || len(*v) == 0 {
		return false
	}
	_, ok := (*v)[a.Unmap()]
	return ok
}

// BlockedAddr accepts a "host:port" or bare-IP string.
func (b *Blocker) BlockedAddr(remote string) bool {
	a, ok := ParseAddr(remote)
	if !ok {
		return false
	}
	return b.Blocked(a)
}

// ParseAddr extracts an address from "1.2.3.4:5678", "[::1]:80" or a bare IP.
func ParseAddr(remote string) (netip.Addr, bool) {
	if remote == "" {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(remote); err == nil {
		return a.Unmap(), true
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		if a, err := netip.ParseAddr(host); err == nil {
			return a.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

// publish rebuilds the lock-free snapshot read by Blocked.
// Caller must hold b.mu (or be in a constructor).
func (b *Blocker) publish() {
	set := make(map[netip.Addr]struct{}, len(b.blocked))
	for a, e := range b.blocked {
		if e.DryRun {
			continue // detected but not enforced
		}
		set[a] = struct{}{}
	}
	b.view.Store(&set)
	b.blockedN.Store(int64(len(set)))
}
