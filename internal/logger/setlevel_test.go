package logger

import (
	"context"
	"log/slog"
	"testing"
)

// The level lives in a slog.LevelVar precisely so it can move, and nothing
// called into it: global.log_level was read once in New and never again, so a
// reload accepted a new value, wrote it to the config, showed it in the panel
// and kept logging at the old threshold until the process restarted.
func TestSetLevelMovesTheThreshold(t *testing.T) {
	log := New("info", "text")
	if got := log.Level(); got != slog.LevelInfo {
		t.Fatalf("initial level = %v, want info", got)
	}

	log.SetLevel("warn")
	if got := log.Level(); got != slog.LevelWarn {
		t.Errorf("after SetLevel(warn) = %v, want warn", got)
	}
	if log.Enabled(context.TODO(), slog.LevelInfo) {
		t.Error("info is still enabled after raising the threshold to warn")
	}
	if !log.Enabled(context.TODO(), slog.LevelError) {
		t.Error("error was disabled by raising the threshold to warn")
	}

	// It has to move both ways: an operator turning debug on mid-incident
	// should not have to restart either.
	log.SetLevel("debug")
	if !log.Enabled(context.TODO(), slog.LevelDebug) {
		t.Error("debug is not enabled after lowering the threshold")
	}
}

// An unrecognised value falls back to info, matching New. Refusing or
// panicking here would let a typo in a reloaded config take the server down.
func TestSetLevelUnknownFallsBackToInfo(t *testing.T) {
	log := New("error", "text")
	log.SetLevel("nonsense")
	if got := log.Level(); got != slog.LevelInfo {
		t.Errorf("unknown level = %v, want info", got)
	}
}
