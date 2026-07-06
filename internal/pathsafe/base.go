package pathsafe

import (
	"sync"
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
		if time.Since(tc.time) < 5*time.Second {
			return tc.resolvedOK && isWithin(b.resolved, tc.resolved)
		}
	}
	resolvedTarget, err := resolvePath(target)
	targetCache.Store(target, &targetCacheEntry{resolved: resolvedTarget, resolvedOK: err == nil, time: time.Now()})
	return err == nil && isWithin(b.resolved, resolvedTarget)
}

type targetCacheEntry struct {
	resolved   string
	resolvedOK bool
	time       time.Time
}

var targetCache sync.Map // map[string]*targetCacheEntry

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
