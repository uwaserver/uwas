package autoblock

import (
	"net/netip"
	"time"
)

// shardFor picks a counter shard from the address bytes. Consecutive addresses
// in a /24 land in different shards, which matters because a flood is usually
// clustered in a handful of netblocks.
func (b *Blocker) shardFor(a netip.Addr) *shard {
	s := a.As16()
	h := uint32(2166136261)
	for _, c := range s {
		h ^= uint32(c)
		h *= 16777619
	}
	return &b.shards[uint8(h)]
}

// withTrack runs fn against the IP's counters, rolling the window first.
// Returns the values fn leaves behind so the caller can evaluate thresholds
// outside the shard lock.
func (b *Blocker) withTrack(a netip.Addr, now time.Time, fn func(t *track)) track {
	sh := b.shardFor(a)
	sh.mu.Lock()
	t, ok := sh.tracks[a]
	if !ok {
		t = &track{windowStart: now}
		sh.tracks[a] = t
	}
	if now.Sub(t.windowStart) >= b.cfg.Window {
		// New window. Concurrent is a live gauge, not a counter, so it
		// deliberately survives the reset.
		c := t.concurrent
		*t = track{windowStart: now, concurrent: c}
	}
	t.lastSeen = now
	fn(t)
	snap := *t
	sh.mu.Unlock()
	return snap
}

// ConnOpened records a new connection and reports whether it should be served.
// A false return means "close it now": either the IP is already blocked, or
// this connection just tripped a threshold.
//
// This is called from the listener for every accepted connection, so it must
// stay allocation-free on the common path.
func (b *Blocker) ConnOpened(a netip.Addr) bool {
	if b == nil || !b.cfg.Enabled {
		return true
	}
	// Safe must win over Blocked. Behind a CDN an edge IP can trip
	// max_connections before cloudflare.ip_ranges is synced; once those
	// ranges land on reload the address becomes Safe, but checking Blocked
	// first would keep refusing it and leave the site offline for everyone
	// that edge serves.
	if b.Safe(a) {
		return true
	}
	if b.Blocked(a) {
		return false
	}

	now := time.Now()
	t := b.withTrack(a, now, func(t *track) {
		t.conns++
		t.concurrent++
	})

	// A refusal here means the caller closes the socket without ever wrapping
	// it, so ConnClosed will not run for this connection. The concurrency
	// slot taken above has to be handed back explicitly — otherwise every
	// refusal leaks one, the gauge never returns to zero, and the counters for
	// that IP become permanently ineligible for garbage collection.
	release := func() {
		b.withTrack(a, time.Now(), func(t *track) {
			if t.concurrent > 0 {
				t.concurrent--
			}
		})
	}

	if b.cfg.MaxConcurrent > 0 && t.concurrent > b.cfg.MaxConcurrent {
		release()
		b.trip(a, ReasonConcurrent, t.concurrent)
		return false
	}
	if b.cfg.MaxConnections > 0 && t.conns > b.cfg.MaxConnections {
		release()
		b.trip(a, ReasonConnFlood, t.conns)
		return false
	}
	return true
}

// ConnClosed releases the concurrency slot. aborted marks a connection that
// closed without the client sending a single byte — the signature of the
// TLS-handshake flood, where the peer connects and hangs up before ClientHello.
// A browser, a bot and a health check all send bytes; only a flood does not.
func (b *Blocker) ConnClosed(a netip.Addr, aborted bool) {
	if b == nil || !b.cfg.Enabled || b.Safe(a) {
		return
	}
	now := time.Now()
	t := b.withTrack(a, now, func(t *track) {
		if t.concurrent > 0 {
			t.concurrent--
		}
		if aborted {
			t.aborts++
		}
	})
	if aborted && t.aborts > b.cfg.MaxAborts {
		b.trip(a, ReasonTLSAbort, t.aborts)
	}
}

// RecordHTTP feeds a request-layer rejection into the counters. reason is one
// of the Reason* constants; unknown reasons are ignored rather than silently
// counted against an unrelated threshold.
func (b *Blocker) RecordHTTP(remote, reason string) {
	if b == nil || !b.cfg.Enabled {
		return
	}
	// Rate-limit rejections only feed the blocker when coupling is on. Off, a
	// 429 stays a soft throttle and never escalates to a hard IP block —
	// important behind NAT, where aggregate 429s would otherwise block a whole
	// gateway.
	if reason == ReasonRate && !b.cfg.FeedRateHits {
		return
	}
	a, ok := ParseAddr(remote)
	if !ok || b.Safe(a) || b.Blocked(a) {
		return
	}

	now := time.Now()
	var limit int
	t := b.withTrack(a, now, func(t *track) {
		switch reason {
		case ReasonWAF:
			t.waf++
		case ReasonRate:
			t.rate++
		case ReasonBot:
			t.waf++ // bot-guard rejections share the WAF budget
		case ReasonNotFound:
			t.notfound++
		}
	})

	var count int
	switch reason {
	case ReasonWAF, ReasonBot:
		count, limit = t.waf, b.cfg.MaxWAFHits
	case ReasonRate:
		count, limit = t.rate, b.cfg.MaxRateHits
	case ReasonNotFound:
		count, limit = t.notfound, b.cfg.MaxNotFound
	default:
		return
	}
	if limit > 0 && count > limit {
		if reason == ReasonBot {
			reason = ReasonWAF
		}
		b.trip(a, reason, count)
	}
}

// trip converts a threshold breach into a block, applying escalation.
func (b *Blocker) trip(a netip.Addr, reason string, hits int) {
	b.detectedN.Add(1)
	b.mu.Lock()
	if e, ok := b.blocked[a]; ok && !e.Expired(time.Now()) {
		b.mu.Unlock()
		return // already blocked; do not re-escalate on every packet
	}
	level := b.history[a] + 1
	dur := b.escalated(level)
	b.mu.Unlock()

	b.apply(a, reason, hits, level, dur)
}

// escalated returns the block duration for an escalation level: the base
// duration quadrupled per repeat offence, capped at MaxBlockDuration.
func (b *Blocker) escalated(level int) time.Duration {
	if !b.cfg.Escalate || level <= 1 {
		return b.cfg.BlockDuration
	}
	d := b.cfg.BlockDuration
	for i := 1; i < level; i++ {
		d *= 4
		if d >= b.cfg.MaxBlockDuration {
			return b.cfg.MaxBlockDuration
		}
	}
	return d
}
