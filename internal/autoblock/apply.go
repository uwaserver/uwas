package autoblock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// apply installs a block. It is the single path through which every block —
// automatic or manual — is written, so persistence, firewall sync and the
// lock-free snapshot can never drift apart.
func (b *Blocker) apply(a netip.Addr, reason string, hits, level int, dur time.Duration) {
	now := time.Now()
	e := &Entry{
		IP:        a.String(),
		Reason:    reason,
		Hits:      hits,
		Level:     level,
		BlockedAt: now,
		ExpiresAt: now.Add(dur),
		DryRun:    b.cfg.DryRun,
	}

	b.mu.Lock()
	b.blocked[a] = e
	b.history[a] = level
	b.publish()
	b.mu.Unlock()

	if b.cfg.DryRun {
		b.log.Warn("autoblock (dry-run, not enforced)",
			"ip", e.IP, "reason", reason, "hits", hits, "would_block_for", dur.String())
		b.requestSave()
		return
	}

	b.log.Warn("autoblock",
		"ip", e.IP, "reason", reason, "hits", hits, "level", level, "duration", dur.String())

	if b.fwEnabled.Load() && b.fwBlock != nil {
		select {
		case b.fwQueue <- fwOp{ip: e.IP}:
		default:
			// Queue full: the in-memory block still holds, we just do not get
			// the kernel-level drop. Losing the rule is better than blocking
			// the accept loop behind a saturated ufw.
			b.log.Warn("autoblock firewall queue full, memory-only block", "ip", e.IP)
		}
	}
	b.requestSave()
}

// Block adds a manual block. dur of 0 uses the configured base duration; a
// negative dur means permanent.
func (b *Blocker) Block(ip, reason string, dur time.Duration) error {
	a, ok := ParseAddr(ip)
	if !ok {
		return fmt.Errorf("invalid IP address: %s", ip)
	}
	if b.Safe(a) {
		return fmt.Errorf("refusing to block %s: address is whitelisted or in a reserved range", a)
	}
	if reason == "" {
		reason = ReasonManual
	}
	if dur == 0 {
		dur = b.cfg.BlockDuration
	}

	now := time.Now()
	e := &Entry{IP: a.String(), Reason: reason, Level: 1, BlockedAt: now}
	if dur > 0 {
		e.ExpiresAt = now.Add(dur)
	}

	b.mu.Lock()
	if prev, exists := b.history[a]; exists {
		e.Level = prev + 1
	}
	b.blocked[a] = e
	b.history[a] = e.Level
	b.publish()
	b.mu.Unlock()

	b.log.Info("manual block", "ip", e.IP, "reason", reason, "duration", dur.String())
	if b.fwEnabled.Load() && b.fwBlock != nil {
		select {
		case b.fwQueue <- fwOp{ip: e.IP}:
		default:
		}
	}
	b.requestSave()
	return nil
}

// Unblock removes a block and clears the escalation history, so an IP an
// operator has deliberately cleared does not come back at level 4.
func (b *Blocker) Unblock(ip string) error {
	a, ok := ParseAddr(ip)
	if !ok {
		return fmt.Errorf("invalid IP address: %s", ip)
	}

	b.mu.Lock()
	_, existed := b.blocked[a]
	delete(b.blocked, a)
	delete(b.history, a)
	b.publish()
	b.mu.Unlock()

	if !existed {
		return fmt.Errorf("%s is not blocked", a)
	}
	b.log.Info("autoblock removed", "ip", a.String())
	if b.fwUnlock != nil {
		select {
		case b.fwQueue <- fwOp{ip: a.String(), remove: true}:
		default:
		}
	}
	b.requestSave()
	return nil
}

// List returns active blocks, newest first.
func (b *Blocker) List() []Entry {
	b.mu.Lock()
	out := make([]Entry, 0, len(b.blocked))
	for _, e := range b.blocked {
		out = append(out, *e)
	}
	b.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].BlockedAt.After(out[j].BlockedAt) })
	return out
}

// Stats summarises the current state for the admin API and metrics.
func (b *Blocker) Stats() map[string]any {
	if b == nil {
		return map[string]any{"enabled": false}
	}
	byReason := map[string]int{}
	b.mu.Lock()
	total := len(b.blocked)
	for _, e := range b.blocked {
		byReason[e.Reason]++
	}
	b.mu.Unlock()

	return map[string]any{
		"enabled":        b.cfg.Enabled,
		"dry_run":        b.cfg.DryRun,
		"firewall_sync":  b.fwEnabled.Load(),
		"active_blocks":  total,
		"total_detected": b.detectedN.Load(),
		"by_reason":      byReason,
		"window":         b.cfg.Window.String(),
		"thresholds": map[string]int{
			"connections": b.cfg.MaxConnections,
			"aborts":      b.cfg.MaxAborts,
			"concurrent":  b.cfg.MaxConcurrent,
			"waf":         b.cfg.MaxWAFHits,
			"rate":        b.cfg.MaxRateHits,
			"notfound":    b.cfg.MaxNotFound,
		},
	}
}

// Start runs expiry, counter GC, firewall synchronisation and state saving
// until ctx is cancelled.
func (b *Blocker) Start(ctx context.Context) {
	if b == nil || !b.cfg.Enabled {
		return
	}
	go b.firewallWorker(ctx)
	go b.saveWorker(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.expire()
			b.gcTracks()
		}
	}
}

// expire lifts blocks whose term is up and releases their firewall rules.
func (b *Blocker) expire() {
	now := time.Now()
	var lifted []string

	b.mu.Lock()
	for a, e := range b.blocked {
		if e.Expired(now) {
			delete(b.blocked, a)
			lifted = append(lifted, e.IP)
		}
	}
	if len(lifted) > 0 {
		b.publish()
	}
	b.mu.Unlock()

	for _, ip := range lifted {
		b.log.Info("autoblock expired", "ip", ip)
		if b.fwUnlock != nil {
			select {
			case b.fwQueue <- fwOp{ip: ip, remove: true}:
			default:
			}
		}
	}
	if len(lifted) > 0 {
		b.requestSave()
	}
}

// gcTracks drops counters for IPs that have gone quiet, so a long-running
// server does not accumulate one map entry per source address ever seen.
func (b *Blocker) gcTracks() {
	cutoff := time.Now().Add(-2 * b.cfg.Window)
	for i := range b.shards {
		sh := &b.shards[i]
		sh.mu.Lock()
		for a, t := range sh.tracks {
			if t.concurrent <= 0 && t.lastSeen.Before(cutoff) {
				delete(sh.tracks, a)
			}
		}
		sh.mu.Unlock()
	}
}

// firewallWorker drains queued ufw operations one at a time. Serialising them
// matters: ufw takes a global lock and concurrent invocations fail outright.
func (b *Blocker) firewallWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case op := <-b.fwQueue:
			b.runFirewallOp(op)
		}
	}
}

func (b *Blocker) runFirewallOp(op fwOp) {
	if op.remove {
		if b.fwUnlock == nil {
			return
		}
		if err := b.fwUnlock(op.ip); err != nil {
			b.log.Debug("autoblock firewall unblock failed", "ip", op.ip, "error", err)
		}
		return
	}
	if b.fwBlock == nil {
		return
	}
	if err := b.fwBlock(op.ip, "uwas-autoblock"); err != nil {
		b.log.Warn("autoblock firewall rule failed, memory-only block still active",
			"ip", op.ip, "error", err)
		return
	}
	b.mu.Lock()
	if a, ok := ParseAddr(op.ip); ok {
		if e := b.blocked[a]; e != nil {
			e.Firewall = true
		}
	}
	b.mu.Unlock()
}

// requestSave coalesces save requests: a flood produces thousands of blocks a
// minute and each one must not become a disk write.
func (b *Blocker) requestSave() {
	select {
	case b.saveOnce <- struct{}{}:
	default:
	}
}

func (b *Blocker) saveWorker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			if dirty {
				b.save()
			}
			return
		case <-b.saveOnce:
			dirty = true
		case <-ticker.C:
			if dirty {
				b.save()
				dirty = false
			}
		}
	}
}

// persisted is the on-disk shape. History is kept alongside the active blocks
// so escalation survives a restart.
type persisted struct {
	Blocks  []Entry        `json:"blocks"`
	History map[string]int `json:"history,omitempty"`
	Saved   time.Time      `json:"saved"`
}

func (b *Blocker) save() {
	if b.cfg.PersistPath == "" {
		return
	}
	b.mu.Lock()
	st := persisted{
		Blocks:  make([]Entry, 0, len(b.blocked)),
		History: make(map[string]int, len(b.history)),
		Saved:   time.Now(),
	}
	for _, e := range b.blocked {
		st.Blocks = append(st.Blocks, *e)
	}
	for a, lvl := range b.history {
		st.History[a.String()] = lvl
	}
	b.mu.Unlock()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	// Write-then-rename so a crash mid-write cannot leave a truncated file
	// that silently drops every active block on the next boot.
	tmp := b.cfg.PersistPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(b.cfg.PersistPath), 0o755); err != nil {
		b.log.Debug("autoblock state dir", "error", err)
		return
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		b.log.Debug("autoblock state write", "error", err)
		return
	}
	if err := os.Rename(tmp, b.cfg.PersistPath); err != nil {
		b.log.Debug("autoblock state rename", "error", err)
		os.Remove(tmp)
	}
}

func (b *Blocker) load() {
	if b.cfg.PersistPath == "" {
		return
	}
	data, err := os.ReadFile(b.cfg.PersistPath)
	if err != nil {
		return
	}
	var st persisted
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}

	now := time.Now()
	b.mu.Lock()
	for i := range st.Blocks {
		e := st.Blocks[i]
		a, ok := ParseAddr(e.IP)
		if !ok || e.Expired(now) || b.Safe(a) {
			continue
		}
		cp := e
		b.blocked[a] = &cp
	}
	for ip, lvl := range st.History {
		if a, ok := ParseAddr(ip); ok {
			b.history[a] = lvl
		}
	}
	b.publish()
	n := len(b.blocked)
	b.mu.Unlock()

	if n > 0 {
		b.log.Info("autoblock state restored", "blocks", n, "path", b.cfg.PersistPath)
	}
}
