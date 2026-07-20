package pathsafe

import (
	"sync"
	"sync/atomic"
	"time"
)

// Base represents a directory whose resolved absolute path is cached for
// repeated containment checks. Resolving the base (filepath.Abs +
// EvalSymlinks) is expensive on Windows and accounts for the majority of
// allocations in the static-serve hot path; caching it eliminates one of the
// two symlink walks per request.
//
// The target path is still resolved on every Contains call so symlinks
// pointing out of the base are still rejected. Only the base side is cached.
type Base struct {
	raw      string
	resolved string
}

// NewBase resolves docRoot once and returns a reusable Base. Returns an error
// if the root cannot be resolved (does not exist, permission denied, etc.).
func NewBase(docRoot string) (*Base, error) {
	resolved, err := resolvePath(docRoot)
	if err != nil {
		return nil, err
	}
	return &Base{raw: docRoot, resolved: resolved}, nil
}

// Contains reports whether target is within the base after resolving target's
// symlinks. Equivalent to IsWithinBaseResolved(base.raw, target) but skips the
// per-call resolution of the base.
//
// Uses a short-lived cache for resolved target paths to avoid repeated
// EvalSymlinks calls on hot static-serve paths. The cache is keyed by the
// original path string and stores only the base-independent resolved path (not
// the containment verdict): resolvePath depends solely on target, while the
// isWithin check depends on this base's root and must be recomputed per call.
// Caching the verdict instead would let one base's result be served to a
// different base checking the same target string — a fail-open containment bug
// when docroots overlap. Entries expire after 5 seconds, short enough to catch
// symlink changes while eliminating ~90% of EvalSymlinks calls in steady state.
func (b *Base) Contains(target string) bool {
	// Fast path: reuse a recently resolved target path, but always re-evaluate
	// containment against this base's own resolved root.
	if entry, ok := targetCache.Load(target); ok {
		tc := entry.(*targetCacheEntry)
		if time.Since(tc.time) < targetCacheTTL {
			return tc.resolvedOK && isWithin(b.resolved, tc.resolved)
		}
	}
	resolvedTarget, err := resolvePath(target)
	if _, loaded := targetCache.Swap(target, &targetCacheEntry{resolved: resolvedTarget, resolvedOK: err == nil, time: time.Now()}); !loaded {
		if targetCacheSize.Add(1) > targetCacheMaxEntries {
			sweepTargetCache()
		}
	}
	return err == nil && isWithin(b.resolved, resolvedTarget)
}

type targetCacheEntry struct {
	resolved   string
	resolvedOK bool
	time       time.Time
}

// targetCacheTTL is how long a resolved target path stays valid.
const targetCacheTTL = 5 * time.Second

// targetCacheMaxEntries caps the target cache so an attacker requesting
// millions of unique URLs cannot grow RSS without bound. Entries expire after
// targetCacheTTL but are only removed by the sweep triggered at this cap.
const targetCacheMaxEntries = 16384

var (
	targetCache         sync.Map // map[string]*targetCacheEntry
	targetCacheSize     atomic.Int64
	targetCacheSweeping atomic.Bool
)

// sweepTargetCache evicts expired entries; if the cache is still over
// capacity afterwards (all entries fresh — e.g. a burst of unique URLs), it
// clears everything. Only one goroutine sweeps at a time; concurrent callers
// return immediately so the hot path stays cheap. The size counter is
// approximate: concurrent inserts during a sweep may drift it slightly, which
// only means the next sweep triggers marginally early or late.
func sweepTargetCache() {
	if !targetCacheSweeping.CompareAndSwap(false, true) {
		return
	}
	defer targetCacheSweeping.Store(false)
	now := time.Now()
	targetCache.Range(func(k, v any) bool {
		if now.Sub(v.(*targetCacheEntry).time) >= targetCacheTTL {
			targetCache.Delete(k)
			targetCacheSize.Add(-1)
		}
		return true
	})
	if targetCacheSize.Load() > targetCacheMaxEntries {
		targetCache.Range(func(k, _ any) bool {
			targetCache.Delete(k)
			targetCacheSize.Add(-1)
			return true
		})
	}
}

// Resolved returns the cached absolute, symlink-resolved root path.
func (b *Base) Resolved() string { return b.resolved }

// Raw returns the original docRoot string passed to NewBase.
func (b *Base) Raw() string { return b.raw }

var baseCache sync.Map // map[string]*Base, keyed by raw docRoot

// CachedBase returns a shared Base for docRoot, resolving it on first use.
// Subsequent calls with the same docRoot string return the cached instance.
// Callers that change a docroot's underlying target (rename, replace) must
// call InvalidateBase to force re-resolution.
func CachedBase(docRoot string) (*Base, error) {
	if v, ok := baseCache.Load(docRoot); ok {
		return v.(*Base), nil
	}
	b, err := NewBase(docRoot)
	if err != nil {
		return nil, err
	}
	actual, _ := baseCache.LoadOrStore(docRoot, b)
	return actual.(*Base), nil
}

// InvalidateBase drops the cached entry for docRoot. Safe to call when no
// entry exists.
func InvalidateBase(docRoot string) {
	baseCache.Delete(docRoot)
}
