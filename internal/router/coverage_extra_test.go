package router

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// --- ReleaseResponseWriter nil branch ---

func TestReleaseResponseWriterNil(t *testing.T) {
	// Must not panic on nil input.
	ReleaseResponseWriter(nil)
}

// --- ResponseWriter.Error after headers written ---

func TestResponseWriterErrorAfterHeaderWritten(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	// Commit headers first.
	w.WriteHeader(201)
	// Error must be a no-op now (headerWritten == true).
	w.Error(500, "too late")

	if w.StatusCode() != 201 {
		t.Errorf("StatusCode() = %d, want 201 (Error should be no-op after header written)", w.StatusCode())
	}
}

// --- normalizeUnknownHost bracketed IPv6 (non-port) branch ---

func TestNormalizeUnknownHostBracketedIPv6(t *testing.T) {
	// "[2001:db8::1]" has no port; SplitHostPort fails, so the bracket-strip
	// branch runs, then ParseAddr recognizes it as an IP → rejected.
	if h, ok := normalizeUnknownHost("[2001:db8::1]"); ok {
		t.Errorf("normalizeUnknownHost([2001:db8::1]) = %q, %v; want rejected as IP", h, ok)
	}
}

func TestNormalizeUnknownHostMultiColonNoBrackets(t *testing.T) {
	// Bare IPv6 with multiple colons: not split, not bracketed, count>1 so the
	// LastIndex branch is skipped. ParseAddr recognizes it → rejected.
	if h, ok := normalizeUnknownHost("2001:db8::1"); ok {
		t.Errorf("normalizeUnknownHost(2001:db8::1) = %q, %v; want rejected as IP", h, ok)
	}
}

func TestNormalizeUnknownHostSingleColonStripBranch(t *testing.T) {
	// "host]:80" makes net.SplitHostPort fail (stray ']'), is not fully
	// bracketed, and has exactly one colon at idx>0 — so the manual
	// single-colon strip branch runs: host becomes "host]" then Trim("[]")
	// yields "host".
	h, ok := normalizeUnknownHost("host]:80")
	if !ok {
		t.Fatalf("normalizeUnknownHost(host]:80) rejected; want accepted")
	}
	if h != "host" {
		t.Errorf("normalizeUnknownHost(host]:80) = %q, want %q", h, "host")
	}
}

// --- loadBlocked: empty filePath early return ---

func TestLoadBlockedEmptyPath(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// SetPersistPath("") sets filePath="" then calls loadBlocked, which must
	// take the empty-path early return without touching disk.
	tracker.SetPersistPath("")
	if tracker.filePath != "" {
		t.Errorf("filePath = %q, want empty", tracker.filePath)
	}
	if len(tracker.blocked) != 0 {
		t.Errorf("blocked map should be empty, got %v", tracker.blocked)
	}
}

// --- Unblock / Dismiss reject invalid (IP/local) hosts via early return ---

func TestUnblockInvalidHostEarlyReturn(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// IP normalizes to invalid → early return, no panic, no effect.
	tracker.Unblock("127.0.0.1")
	if len(tracker.blocked) != 0 {
		t.Errorf("blocked map should remain empty, got %v", tracker.blocked)
	}
}

func TestDismissInvalidHostEarlyReturn(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("keep.com")
	// IP normalizes to invalid → early return, keep.com untouched.
	tracker.Dismiss("localhost")
	if len(tracker.List()) != 1 {
		t.Errorf("expected keep.com to remain, got %d entries", len(tracker.List()))
	}
}

// --- List skips entries whose key fails normalization (defensive branch) ---

func TestListSkipsUnnormalizableKey(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("good.com")
	// Directly inject a key that fails normalizeUnknownHost (an IP), simulating
	// a corrupted map. List must skip it.
	tracker.mu.Lock()
	tracker.hosts["127.0.0.1"] = &UnknownHostEntry{Host: "127.0.0.1", Hits: 99}
	tracker.mu.Unlock()

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (bad key skipped), got %d: %#v", len(entries), entries)
	}
	if entries[0].Host != "good.com" {
		t.Errorf("remaining host = %q, want good.com", entries[0].Host)
	}
}

// --- BlockedHosts skips entries whose key fails normalization ---

func TestBlockedHostsSkipsUnnormalizableKey(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("evil.com")
	tracker.mu.Lock()
	tracker.blocked["127.0.0.1"] = true
	tracker.mu.Unlock()

	hosts := tracker.BlockedHosts()
	if len(hosts) != 1 || hosts[0] != "evil.com" {
		t.Errorf("BlockedHosts = %v, want [evil.com] (bad key skipped)", hosts)
	}
}

// --- saveBlocked: os.Create failure branch ---

func TestSaveBlockedCreateError(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// Point the persist path at a path whose parent does not exist so os.Create
	// fails; saveBlocked must return without panicking.
	tracker.filePath = filepath.Join(t.TempDir(), "nonexistent-dir", "blocked.txt")
	tracker.Block("evil.com") // triggers saveBlocked internally
	// State should still reflect the block in memory.
	if !tracker.IsBlocked("evil.com") {
		t.Error("evil.com should still be blocked in memory despite save failure")
	}
}

// --- loadBlocked: skips blank lines and comments ---

func TestLoadBlockedSkipsBlankAndComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocked.txt")
	content := "\n# this is a comment\n   \nevil.com\n# another comment\nspam.net\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tracker := NewUnknownHostTracker()
	tracker.SetPersistPath(path)

	if !tracker.IsBlocked("evil.com") || !tracker.IsBlocked("spam.net") {
		t.Error("evil.com and spam.net should be loaded")
	}
	hosts := tracker.BlockedHosts()
	if len(hosts) != 2 {
		t.Errorf("expected 2 blocked hosts (blank/comment lines skipped), got %d: %v", len(hosts), hosts)
	}
}

// --- registerExactHost: empty host early return ---

func TestRegisterExactHostEmpty(t *testing.T) {
	// A domain whose host trims to "" (whitespace + trailing dot) and whose
	// alias is also empty exercises the empty-host early return.
	domains := []config.Domain{
		{Host: "  .  ", Aliases: []string{"   ", "real.com"}, Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	// "real.com" alias should still be registered.
	if d := r.Lookup("real.com"); d == nil {
		t.Error("real.com alias should be registered despite empty host")
	}
}
