package backup

import (
	"fmt"
	"testing"
)

// m.keepCount is written by SetKeepCount under m.mu, but pruneOld used to read
// it with no lock at all. pruneOld runs on the backup goroutine — CreateBackup
// releases m.mu before calling it (backup.go:274-279) — while the admin API
// updates the retention count from an HTTP goroutine
// (internal/admin/backup/handler.go:309-311). A write under a mutex and a read
// with no mutex are not ordered by that mutex, so that is a data race.
//
// The race also enabled a crash: the length check and the slice were two
// separate reads of the field, so keepCount could grow past len(fulls) between
// them and make fulls[keepCount:] panic with slice bounds out of range.
//
// This is a -race test. It is a no-op assertion under a plain `go test`; run it
// with the race detector to see it fail without the fix:
//
//	go test -race -run TestPruneOldKeepCount ./internal/backup/
func TestPruneOldKeepCountConcurrentWithSetKeepCount(t *testing.T) {
	m, mp := testManager(t)
	seedRetentionBackups(mp, 6)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 1; i <= 2000; i++ {
			m.SetKeepCount(1 + i%5)
		}
	}()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for i := 0; i < 2000; i++ {
			seedRetentionBackups(mp, 6) // keep a non-empty list to prune
			m.pruneOld("mem")
		}
	}()

	<-writerDone
	<-readerDone
}

// TestPruneOldConcurrentWithLockedAccessors is the control: ScheduleDetail and
// SetKeepCount both take m.mu, so running them against each other stays
// race-free. If this test ever reports a race, the harness rather than
// pruneOld is at fault.
func TestPruneOldConcurrentWithLockedAccessors(t *testing.T) {
	m, _ := testManager(t)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 1; i <= 2000; i++ {
			m.SetKeepCount(1 + i%5)
		}
	}()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for i := 0; i < 2000; i++ {
			_ = m.ScheduleDetail()
		}
	}()

	<-writerDone
	<-readerDone
}

// TestPruneOldRespectsKeepCount pins the behaviour the snapshot must preserve,
// deterministically and without -race: exactly keepCount full backups survive,
// the newest ones.
func TestPruneOldRespectsKeepCount(t *testing.T) {
	m, mp := testManager(t)
	m.SetKeepCount(2)
	seedRetentionBackups(mp, 5)

	m.pruneOld("mem")

	mp.mu.Lock()
	defer mp.mu.Unlock()
	if len(mp.files) != 2 {
		names := make([]string, 0, len(mp.files))
		for n := range mp.files {
			names = append(names, n)
		}
		t.Errorf("kept %d full backups, want 2: %v", len(mp.files), names)
	}
}

// TestPruneOldKeepsPerDomainBackups is the neighbouring control: retention must
// not touch uwas-domain-* archives, which have their own lifecycle.
func TestPruneOldKeepsPerDomainBackups(t *testing.T) {
	m, mp := testManager(t)
	m.SetKeepCount(1)
	seedRetentionBackups(mp, 3)

	mp.mu.Lock()
	mp.files["uwas-domain-example.com-20260101-000000.tar.gz"] = []byte("domain")
	mp.mu.Unlock()

	m.pruneOld("mem")

	mp.mu.Lock()
	defer mp.mu.Unlock()
	if _, ok := mp.files["uwas-domain-example.com-20260101-000000.tar.gz"]; !ok {
		t.Error("retention deleted a per-domain backup")
	}
	if len(mp.files) != 2 { // 1 kept full + the domain archive
		t.Errorf("expected 1 full backup plus the domain archive, got %d files", len(mp.files))
	}
}

// TestPruneOldUnderLimitDeletesNothing is the boundary case: when the number of
// full backups is at or below keepCount, nothing is removed.
func TestPruneOldUnderLimitDeletesNothing(t *testing.T) {
	m, mp := testManager(t)
	m.SetKeepCount(5)
	seedRetentionBackups(mp, 5)

	m.pruneOld("mem")

	mp.mu.Lock()
	defer mp.mu.Unlock()
	if len(mp.files) != 5 {
		t.Errorf("deleted %d backups at the keepCount limit, want none", 5-len(mp.files))
	}
}

// seedRetentionBackups fills the memory provider with full backups, which are
// the only entries pruneOld considers.
func seedRetentionBackups(mp *memoryProvider, n int) {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	for i := 0; i < n; i++ {
		mp.files[fmt.Sprintf("uwas-backup-2026010%d-000000.tar.gz", i+1)] = []byte("payload")
	}
}
