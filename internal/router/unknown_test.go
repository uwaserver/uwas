package router

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

func TestNewUnknownHostTracker(t *testing.T) {
	tracker := NewUnknownHostTracker()
	if tracker == nil {
		t.Fatal("NewUnknownHostTracker returned nil")
	}
	if tracker.hosts == nil {
		t.Error("hosts map should be initialized")
	}
	if tracker.blocked == nil {
		t.Error("blocked map should be initialized")
	}
}

// TestSetPersistPath tests setting persistence path.
func TestSetPersistPath(t *testing.T) {
	tmpDir := t.TempDir()
	persistFile := filepath.Join(tmpDir, "blocked.txt")

	tracker := NewUnknownHostTracker()
	tracker.SetPersistPath(persistFile)

	if tracker.filePath != persistFile {
		t.Errorf("filePath = %q, want %q", tracker.filePath, persistFile)
	}
}

// TestSetPersistPathWithExistingFile tests loading blocked hosts from existing file.
func TestSetPersistPathWithExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	persistFile := filepath.Join(tmpDir, "blocked.txt")

	// Create a file with blocked hosts
	content := "evil.com\nspammer.net\n"
	if err := os.WriteFile(persistFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create persist file: %v", err)
	}

	tracker := NewUnknownHostTracker()
	tracker.SetPersistPath(persistFile)

	// Check that blocked hosts were loaded
	if !tracker.IsBlocked("evil.com") {
		t.Error("evil.com should be blocked after loading from file")
	}
	if !tracker.IsBlocked("spammer.net") {
		t.Error("spammer.net should be blocked after loading from file")
	}
}

// TestSetPersistPathPersistence tests that blocked hosts are persisted.
func TestSetPersistPathPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	persistFile := filepath.Join(tmpDir, "blocked.txt")

	tracker := NewUnknownHostTracker()
	tracker.SetPersistPath(persistFile)

	// Block some hosts
	tracker.Block("evil.com")
	tracker.Block("spammer.net")

	// Create a new tracker and load from the same file
	tracker2 := NewUnknownHostTracker()
	tracker2.SetPersistPath(persistFile)

	// Check that blocked hosts were persisted
	if !tracker2.IsBlocked("evil.com") {
		t.Error("evil.com should be persisted and loaded")
	}
	if !tracker2.IsBlocked("spammer.net") {
		t.Error("spammer.net should be persisted and loaded")
	}
}

// --- Record ---

func TestRecordNewHost(t *testing.T) {
	tracker := NewUnknownHostTracker()
	blocked := tracker.Record("unknown.com")
	if blocked {
		t.Error("new host should not be blocked")
	}

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Host != "unknown.com" {
		t.Errorf("host = %q, want unknown.com", entries[0].Host)
	}
	if entries[0].Hits != 1 {
		t.Errorf("hits = %d, want 1", entries[0].Hits)
	}
	if entries[0].Blocked {
		t.Error("entry should not be blocked")
	}
	if entries[0].FirstSeen.IsZero() {
		t.Error("FirstSeen should be set")
	}
	if entries[0].LastSeen.IsZero() {
		t.Error("LastSeen should be set")
	}
}

func TestRecordExistingHostIncrementsHits(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("example.com")
	tracker.Record("example.com")
	tracker.Record("example.com")

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Hits != 3 {
		t.Errorf("hits = %d, want 3", entries[0].Hits)
	}
}

func TestRecordStripsPort(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("example.com:8080")

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Host != "example.com" {
		t.Errorf("host = %q, want example.com (port stripped)", entries[0].Host)
	}
}

func TestRecordCaseInsensitive(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("Example.COM")
	tracker.Record("example.com")

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (case-insensitive), got %d", len(entries))
	}
	if entries[0].Hits != 2 {
		t.Errorf("hits = %d, want 2", entries[0].Hits)
	}
}

func TestRecordEmptyHost(t *testing.T) {
	tracker := NewUnknownHostTracker()
	blocked := tracker.Record("")
	if blocked {
		t.Error("empty host should not be blocked")
	}
	entries := tracker.List()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty host, got %d", len(entries))
	}
}

func TestRecordEmptyAfterPortStrip(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// ":8080" strips to "" which should be rejected
	blocked := tracker.Record(":8080")
	if blocked {
		t.Error("empty host (after port strip) should not be blocked")
	}
	entries := tracker.List()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestRecordBlockedHostReturnsTrue(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("evil.com")

	// First record of a pre-blocked host
	blocked := tracker.Record("evil.com")
	if !blocked {
		t.Error("Record should return true for blocked host")
	}

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Blocked {
		t.Error("entry should be marked as blocked")
	}
}

func TestRecordExistingBlockedHostReturnsTrue(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// Record first, then block, then record again
	tracker.Record("evil.com")
	tracker.Block("evil.com")

	blocked := tracker.Record("evil.com")
	if !blocked {
		t.Error("Record should return true for existing blocked host")
	}
}

func TestRecordUpdatesLastSeen(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("example.com")

	entries1 := tracker.List()
	first := entries1[0].LastSeen

	// Record again
	tracker.Record("example.com")
	entries2 := tracker.List()
	second := entries2[0].LastSeen

	if second.Before(first) {
		t.Error("LastSeen should not go backwards")
	}
}

// --- IsBlocked ---

func TestIsBlockedFalseByDefault(t *testing.T) {
	tracker := NewUnknownHostTracker()
	if tracker.IsBlocked("anything.com") {
		t.Error("should not be blocked by default")
	}
}

func TestIsBlockedTrueAfterBlock(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("evil.com")
	if !tracker.IsBlocked("evil.com") {
		t.Error("should be blocked after Block()")
	}
}

func TestIsBlockedStripsPort(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("evil.com")
	if !tracker.IsBlocked("evil.com:443") {
		t.Error("IsBlocked should strip port before checking")
	}
}

func TestIsBlockedCaseInsensitive(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("Evil.COM")
	if !tracker.IsBlocked("evil.com") {
		t.Error("IsBlocked should be case-insensitive")
	}
}

// --- Block ---

func TestBlockNewHost(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("evil.com")

	if !tracker.IsBlocked("evil.com") {
		t.Error("host should be blocked after Block()")
	}
	blocked := tracker.BlockedHosts()
	if len(blocked) != 1 || blocked[0] != "evil.com" {
		t.Errorf("BlockedHosts = %v, want [evil.com]", blocked)
	}
}

func TestBlockExistingTrackedHost(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("evil.com")
	tracker.Block("evil.com")

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Blocked {
		t.Error("entry should be marked blocked after Block()")
	}
}

func TestBlockCaseInsensitive(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("EVIL.COM")
	if !tracker.IsBlocked("evil.com") {
		t.Error("Block should normalize to lowercase")
	}
}

func TestBlockUpdatesExistingEntry(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// Record the host first, so it has an entry
	tracker.Record("spam.com")
	// Verify it's not blocked initially
	entries := tracker.List()
	if entries[0].Blocked {
		t.Fatal("should not be blocked initially")
	}
	// Block it
	tracker.Block("spam.com")
	entries = tracker.List()
	if !entries[0].Blocked {
		t.Error("existing entry should be updated to blocked")
	}
}

// --- Unblock ---

func TestUnblockHost(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("evil.com")
	tracker.Unblock("evil.com")

	if tracker.IsBlocked("evil.com") {
		t.Error("host should not be blocked after Unblock()")
	}
}

func TestUnblockUpdatesExistingEntry(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("spam.com")
	tracker.Block("spam.com")

	entries := tracker.List()
	if !entries[0].Blocked {
		t.Fatal("should be blocked")
	}

	tracker.Unblock("spam.com")
	entries = tracker.List()
	if entries[0].Blocked {
		t.Error("entry should be updated to unblocked")
	}
}

func TestUnblockNonExistentHost(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// Should not panic
	tracker.Unblock("nonexistent.com")
	if tracker.IsBlocked("nonexistent.com") {
		t.Error("nonexistent host should not be blocked")
	}
}

func TestUnblockCaseInsensitive(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("evil.com")
	tracker.Unblock("EVIL.COM")
	if tracker.IsBlocked("evil.com") {
		t.Error("Unblock should be case-insensitive")
	}
}

func TestUnblockWithoutEntry(t *testing.T) {
	// Block without ever recording, then unblock
	tracker := NewUnknownHostTracker()
	tracker.Block("ghost.com")
	tracker.Unblock("ghost.com")
	if tracker.IsBlocked("ghost.com") {
		t.Error("should be unblocked")
	}
}

// --- Dismiss ---

func TestDismissRemovesEntry(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("temp.com")
	tracker.Dismiss("temp.com")

	entries := tracker.List()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after dismiss, got %d", len(entries))
	}
}

func TestDismissRemovesBlockedStatus(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("evil.com")
	tracker.Block("evil.com")
	tracker.Dismiss("evil.com")

	if tracker.IsBlocked("evil.com") {
		t.Error("Dismiss should remove blocked status")
	}
	entries := tracker.List()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after dismiss, got %d", len(entries))
	}
}

func TestDismissNonExistentHost(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// Should not panic
	tracker.Dismiss("nonexistent.com")
}

func TestDismissCaseInsensitive(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("Temp.COM")
	tracker.Dismiss("temp.com")
	entries := tracker.List()
	if len(entries) != 0 {
		t.Errorf("Dismiss should be case-insensitive, got %d entries", len(entries))
	}
}

// --- List ---

func TestListEmpty(t *testing.T) {
	tracker := NewUnknownHostTracker()
	entries := tracker.List()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestListSortedByHitsDescending(t *testing.T) {
	tracker := NewUnknownHostTracker()

	// Record different hit counts
	tracker.Record("low.com")    // 1 hit
	tracker.Record("mid.com")    // 2 hits
	tracker.Record("mid.com")
	tracker.Record("high.com")   // 3 hits
	tracker.Record("high.com")
	tracker.Record("high.com")

	entries := tracker.List()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Host != "high.com" || entries[0].Hits != 3 {
		t.Errorf("first entry = %v, want high.com with 3 hits", entries[0])
	}
	if entries[1].Host != "mid.com" || entries[1].Hits != 2 {
		t.Errorf("second entry = %v, want mid.com with 2 hits", entries[1])
	}
	if entries[2].Host != "low.com" || entries[2].Hits != 1 {
		t.Errorf("third entry = %v, want low.com with 1 hit", entries[2])
	}
}

func TestListReturnsCopy(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("example.com")

	entries := tracker.List()
	entries[0].Hits = 999

	// Original should not be affected
	entries2 := tracker.List()
	if entries2[0].Hits == 999 {
		t.Error("List should return copies, not references")
	}
}

// --- BlockedHosts ---

func TestBlockedHostsEmpty(t *testing.T) {
	tracker := NewUnknownHostTracker()
	hosts := tracker.BlockedHosts()
	if len(hosts) != 0 {
		t.Errorf("expected 0 blocked hosts, got %d", len(hosts))
	}
}

func TestBlockedHostsSorted(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("zebra.com")
	tracker.Block("alpha.com")
	tracker.Block("middle.com")

	hosts := tracker.BlockedHosts()
	if len(hosts) != 3 {
		t.Fatalf("expected 3 blocked hosts, got %d", len(hosts))
	}
	if !sort.StringsAreSorted(hosts) {
		t.Errorf("BlockedHosts should be sorted, got %v", hosts)
	}
	if hosts[0] != "alpha.com" || hosts[1] != "middle.com" || hosts[2] != "zebra.com" {
		t.Errorf("unexpected order: %v", hosts)
	}
}

func TestBlockedHostsAfterUnblock(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Block("a.com")
	tracker.Block("b.com")
	tracker.Unblock("a.com")

	hosts := tracker.BlockedHosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 blocked host, got %d", len(hosts))
	}
	if hosts[0] != "b.com" {
		t.Errorf("expected b.com, got %q", hosts[0])
	}
}

// --- Concurrent access ---

func TestConcurrentRecordAndList(t *testing.T) {
	tracker := NewUnknownHostTracker()
	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracker.Record("concurrent.com")
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracker.List()
				tracker.IsBlocked("concurrent.com")
				tracker.BlockedHosts()
			}
		}()
	}

	wg.Wait()

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Hits != 5000 {
		t.Errorf("hits = %d, want 5000", entries[0].Hits)
	}
}

func TestConcurrentBlockAndRecord(t *testing.T) {
	tracker := NewUnknownHostTracker()
	var wg sync.WaitGroup

	// Block while recording concurrently
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			tracker.Record("race.com")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			tracker.Block("race.com")
			tracker.Unblock("race.com")
		}
	}()

	wg.Wait()
	// No data race = pass
}

func TestConcurrentDismiss(t *testing.T) {
	tracker := NewUnknownHostTracker()
	var wg sync.WaitGroup

	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			tracker.Record("dismiss-race.com")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			tracker.Dismiss("dismiss-race.com")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			tracker.List()
		}
	}()

	wg.Wait()
	// No data race = pass
}

// --- Edge cases ---

func TestRecordMultipleHosts(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("a.com")
	tracker.Record("b.com")
	tracker.Record("c.com")

	entries := tracker.List()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

func TestBlockHostNotPreviouslyRecorded(t *testing.T) {
	tracker := NewUnknownHostTracker()
	// Block a host that was never recorded
	tracker.Block("preblock.com")

	// Now record it
	blocked := tracker.Record("preblock.com")
	if !blocked {
		t.Error("Record should return true for pre-blocked host")
	}
}

func TestDismissOnlyRemovesTarget(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("keep.com")
	tracker.Record("remove.com")
	tracker.Block("keep.com")
	tracker.Block("remove.com")

	tracker.Dismiss("remove.com")

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Host != "keep.com" {
		t.Errorf("remaining host = %q, want keep.com", entries[0].Host)
	}
	if !tracker.IsBlocked("keep.com") {
		t.Error("keep.com should still be blocked")
	}
	if tracker.IsBlocked("remove.com") {
		t.Error("remove.com should no longer be blocked")
	}
}

func TestRecordWithPortAndCase(t *testing.T) {
	tracker := NewUnknownHostTracker()
	tracker.Record("Example.COM:443")
	tracker.Record("EXAMPLE.com:8080")
	tracker.Record("example.com")

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (all same host), got %d", len(entries))
	}
	if entries[0].Hits != 3 {
		t.Errorf("hits = %d, want 3", entries[0].Hits)
	}
	if entries[0].Host != "example.com" {
		t.Errorf("host = %q, want example.com", entries[0].Host)
	}
}

func TestFullLifecycle(t *testing.T) {
	tracker := NewUnknownHostTracker()

	// Record some hosts
	tracker.Record("a.com")
	tracker.Record("a.com")
	tracker.Record("b.com")
	tracker.Record("c.com")

	// Block one
	tracker.Block("b.com")
	if !tracker.IsBlocked("b.com") {
		t.Error("b.com should be blocked")
	}

	// Record blocked host returns true
	if !tracker.Record("b.com") {
		t.Error("Record(b.com) should return true when blocked")
	}

	// Unblock
	tracker.Unblock("b.com")
	if tracker.IsBlocked("b.com") {
		t.Error("b.com should be unblocked")
	}

	// Dismiss
	tracker.Dismiss("c.com")
	entries := tracker.List()
	if len(entries) != 2 {
		t.Errorf("expected 2 entries after dismiss, got %d", len(entries))
	}

	// Verify order (a.com has 2 hits, b.com has 2 hits)
	for _, e := range entries {
		if e.Host == "c.com" {
			t.Error("c.com should have been dismissed")
		}
	}
}
