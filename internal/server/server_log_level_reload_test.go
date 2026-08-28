package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// global.log_level was read once at startup and never again. A reload accepted
// a new value, wrote it to the config and showed it in the panel while the
// process kept logging at the old threshold — the operator had to restart to
// quieten a noisy server, which is the moment they least want to.

func writeReloadConfig(t *testing.T, level string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "uwas.yaml")
	yaml := "global:\n  log_level: " + level + "\n  log_format: text\n  worker_count: \"1\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestReloadAppliesLogLevel(t *testing.T) {
	s := newDispatchTestServer(t, nil)
	s.logger.SetLevel("info")

	s.configPath = writeReloadConfig(t, "warn")
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := s.logger.Level(); got != slog.LevelWarn {
		t.Errorf("level after reload = %v, want warn — the new value did not reach the logger", got)
	}
	if s.logger.Enabled(context.TODO(), slog.LevelInfo) {
		t.Error("info is still enabled after reloading with log_level: warn")
	}
}

// It has to move both ways: turning debug on mid-incident must not need a
// restart either.
func TestReloadLowersLogLevel(t *testing.T) {
	s := newDispatchTestServer(t, nil)
	s.logger.SetLevel("error")

	s.configPath = writeReloadConfig(t, "debug")
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if !s.logger.Enabled(context.TODO(), slog.LevelDebug) {
		t.Error("debug is not enabled after reloading with log_level: debug")
	}
}

// The global access-log switch is a pointer so an absent block keeps the
// request stream on, which is what has always happened.
func TestGlobalAccessLogDefaultsOn(t *testing.T) {
	if !(config.GlobalAccessLog{}).RequestLogEnabled() {
		t.Error("an absent access_log block turned the request log off")
	}
	if !(config.GlobalAccessLog{Enabled: config.BoolPtr(true)}).RequestLogEnabled() {
		t.Error("an explicit true turned it off")
	}
	if (config.GlobalAccessLog{Enabled: config.BoolPtr(false)}).RequestLogEnabled() {
		t.Error("an explicit false did not turn it off")
	}
}
