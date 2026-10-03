package backup

// Regression: the cron scheduler must fire on EVERY matching minute, not just
// the first one.
//
// Bug (fixed in the timer-rearm drain of ScheduleBackupCron's goroutine): the
// loop re-armed its timer with an unconditional drain,
//
//	if !timer.Stop() { <-timer.C }
//
// but on the iteration after a fire, the select had already consumed the fired
// value. Stop then returned false and the drain blocked forever, so the
// schedule fired exactly once per process while ScheduleStatus kept reporting
// active=true. The first scheduled backup of any cron expression ran; every
// later one never did — e.g. a "0 2 * * *" nightly backup became a one-shot
// on process start day.
//
// The smallest cron granularity is one minute, so this test waits across a
// real minute boundary twice (~2.5 min). There is no clock seam in
// ScheduleBackupCron; TestScheduleBackupCronGoroutineFires already accepted
// this trade-off for a single fire. Skipped under -short like that test.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScheduleBackupCronFiresOnEveryMatchingMinute(t *testing.T) {
	if testing.Short() {
		t.Skip("real-minute cron scheduling; skipped in -short")
	}
	m, _ := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	if err := os.WriteFile(cfgFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	m.SetPaths(cfgFile, "")

	fired := make(chan struct{}, 4)
	m.SetOnBackup(func(info *BackupInfo, err error) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	t.Cleanup(m.Stop)

	// Control: the first fire must arrive within ~a minute (next minute
	// boundary, 70s window covers worst-case start position + slack). If this
	// never arrives the schedule or harness is broken, not the rearm bug.
	m.ScheduleBackupCron("* * * * *")
	select {
	case <-fired:
	case <-time.After(70 * time.Second):
		t.Fatal("first scheduled backup never fired — schedule did not start")
	}

	// The regression: the second fire must arrive ~60s later. Before the fix
	// the scheduler goroutine was wedged in the timer-rearm drain and this
	// never happened, while ScheduleStatus still reported active.
	select {
	case <-fired:
	case <-time.After(75 * time.Second):
		_, active := m.ScheduleStatus()
		t.Fatalf("second scheduled backup never fired (schedule still reports active=%v) — cron scheduler wedged after its first fire", active)
	}
}
