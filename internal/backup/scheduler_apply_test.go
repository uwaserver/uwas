package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2920: the scheduler goroutines captured the default provider name when the
// schedule started, so a reload that changed backup.provider kept sending the
// scheduled backups to the old destination.
// F2921: a schedule changed or removed in the config was applied only at
// startup; ApplySchedule makes the reload follow it without restarting a
// schedule that did not change.

func scheduledManager(t *testing.T) (*BackupManager, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(config.BackupConfig{Provider: "local", Keep: 3, Local: config.BackupLocalConfig{Path: filepath.Join(dir, "a")}}, logger.New("error", "text"))
	m.SetPaths(cfgPath, "")
	t.Cleanup(m.Stop)
	return m, dir
}

type scheduledRun struct {
	info *BackupInfo
	err  error
}

func collectRuns(m *BackupManager) chan scheduledRun {
	ch := make(chan scheduledRun, 256)
	m.SetOnBackup(func(info *BackupInfo, err error) {
		select {
		case ch <- scheduledRun{info, err}:
		default:
		}
	})
	return ch
}

func TestSchedulerFollowsReconfiguredProvider(t *testing.T) {
	m, dir := scheduledManager(t)
	runs := collectRuns(m)
	m.ScheduleBackup(40 * time.Millisecond)

	select {
	case r := <-runs:
		if r.err != nil || r.info == nil || r.info.Provider != "local" {
			t.Fatalf("control run = %+v, want success on local", r)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no scheduled run")
	}

	m.Reconfigure(config.BackupConfig{Provider: "sftp", Keep: 3,
		Local: config.BackupLocalConfig{Path: filepath.Join(dir, "a")},
		SFTP:  config.BackupSFTPConfig{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", RemotePath: "/x", InsecureKnownHosts: true}})

	deadline := time.After(30 * time.Second)
	for {
		select {
		case r := <-runs:
			if r.err != nil {
				return // the scheduled run used the new (unreachable) provider
			}
		case <-deadline:
			t.Fatal("scheduled runs kept using the old provider after Reconfigure")
		}
	}
}

func TestSchedulerCronFollowsReconfiguredProvider(t *testing.T) {
	m, dir := scheduledManager(t)
	m.Reconfigure(config.BackupConfig{Provider: "sftp", Keep: 3,
		Local: config.BackupLocalConfig{Path: filepath.Join(dir, "a")},
		SFTP:  config.BackupSFTPConfig{Host: "127.0.0.1", Port: 1, User: "u", Password: "p", RemotePath: "/x", InsecureKnownHosts: true}})
	if got := m.defaultProvider(); got != "sftp" {
		t.Fatalf("defaultProvider = %q, want sftp", got)
	}
	m.Reconfigure(config.BackupConfig{})
	if got := m.defaultProvider(); got != "local" {
		t.Fatalf("empty provider must default to local, got %q", got)
	}
}

func TestApplyScheduleTransitions(t *testing.T) {
	m, _ := scheduledManager(t)

	m.ApplySchedule("", "")
	if _, on := m.ScheduleStatus(); on {
		t.Fatal("no schedule configured, but the scheduler is active")
	}
	m.ApplySchedule("", "1h")
	if iv, on := m.ScheduleStatus(); !on || iv != time.Hour {
		t.Fatalf("interval 1h: got %v active=%v", iv, on)
	}
	m.ApplySchedule("", "30m")
	if iv, on := m.ScheduleStatus(); !on || iv != 30*time.Minute {
		t.Fatalf("interval 30m: got %v active=%v", iv, on)
	}
	m.ApplySchedule("0 2 * * *", "30m") // cron wins over the interval
	if _, on := m.ScheduleStatus(); !on {
		t.Fatal("cron: scheduler not active")
	}
	if m.cronExpr != "0 2 * * *" {
		t.Fatalf("cronExpr = %q", m.cronExpr)
	}
	m.ApplySchedule("", "45m") // cron removed, interval back
	if iv, on := m.ScheduleStatus(); !on || iv != 45*time.Minute || m.cronExpr != "" {
		t.Fatalf("interval after cron: %v active=%v cron=%q", iv, on, m.cronExpr)
	}
	m.ApplySchedule("", "bogus") // unparseable: nothing to run
	if _, on := m.ScheduleStatus(); on {
		t.Fatal("unparseable schedule must stop the scheduler")
	}
	m.ApplySchedule("", "-5m")
	if _, on := m.ScheduleStatus(); on {
		t.Fatal("negative schedule must not start the scheduler")
	}
}

// An unchanged schedule must not be restarted: re-applying it on every reload
// would reset the ticker and a 24h interval would never fire if reloads are
// more frequent than that.
func TestApplyScheduleUnchangedDoesNotRestart(t *testing.T) {
	m, _ := scheduledManager(t)
	runs := collectRuns(m)
	m.ApplySchedule("", "60ms")

	got := 0
	deadline := time.After(30 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for got < 2 {
		select {
		case <-tick.C:
			m.ApplySchedule("", "60ms") // a reload with the same schedule
		case <-runs:
			got++
		case <-deadline:
			t.Fatalf("only %d scheduled runs; re-applying the same schedule keeps resetting the ticker", got)
		}
	}
}

// A slow upload must not hold the manager lock: the admin API (list, schedule
// detail) and a reload (Reconfigure) have to keep answering meanwhile.
type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingProvider) Name() string { return "local" }
func (b *blockingProvider) Upload(ctx context.Context, filename string, data io.Reader) error {
	close(b.entered)
	<-b.release
	_, _ = io.Copy(io.Discard, data)
	return nil
}
func (b *blockingProvider) Download(context.Context, string) (io.ReadCloser, error) {
	return nil, os.ErrNotExist
}
func (b *blockingProvider) List(context.Context) ([]BackupInfo, error) { return nil, nil }
func (b *blockingProvider) Delete(context.Context, string) error       { return nil }

func TestSlowUploadDoesNotBlockManagerAPI(t *testing.T) {
	m, _ := scheduledManager(t)
	bp := &blockingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	m.mu.Lock()
	m.providers["local"] = bp
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() { _, err := m.CreateBackup("local"); done <- err }()
	select {
	case <-bp.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("upload never started")
	}

	answered := make(chan struct{})
	go func() {
		_ = m.ListBackups()
		_ = m.ScheduleDetail()
		m.Reconfigure(config.BackupConfig{Provider: "local", Keep: 5, Local: config.BackupLocalConfig{Path: t.TempDir()}})
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(15 * time.Second):
		t.Error("manager API blocked while an upload was in flight")
	}
	close(bp.release)
	if err := <-done; err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
}
