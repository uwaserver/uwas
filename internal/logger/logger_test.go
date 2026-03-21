package logger

import (
	"testing"
)

func TestNewLogger(t *testing.T) {
	log := New("info", "text")
	if log == nil {
		t.Fatal("New returned nil")
	}

	// Should not panic
	log.Info("test message", "key", "value")
}

func TestLogLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error"}
	for _, level := range levels {
		log := New(level, "json")
		if log == nil {
			t.Fatalf("New(%q) returned nil", level)
		}
	}
}

func TestSetLevel(t *testing.T) {
	log := New("info", "text")
	log.SetLevel("debug")
	// Should not panic
	log.Debug("debug message after level change")
}

func TestStdLogger(t *testing.T) {
	log := New("info", "text")
	stdLog := log.StdLogger()
	if stdLog == nil {
		t.Fatal("StdLogger returned nil")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"debug", "DEBUG"},
		{"info", "INFO"},
		{"warn", "WARN"},
		{"warning", "WARN"},
		{"error", "ERROR"},
		{"unknown", "INFO"}, // defaults to info
	}

	for _, tt := range tests {
		got := parseLevel(tt.input)
		if got.String() != tt.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
