package logger

import (
	"log/slog"
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

func TestWriter(t *testing.T) {
	log := New("debug", "text")
	w := log.Writer(slog.LevelError)
	if w == nil {
		t.Fatal("Writer returned nil")
	}

	// Write should return the correct byte count and no error
	msg := []byte("test error message\n")
	n, err := w.Write(msg)
	if err != nil {
		t.Fatalf("Writer.Write returned error: %v", err)
	}
	if n != len(msg) {
		t.Errorf("Writer.Write returned %d, want %d", n, len(msg))
	}

	// Also test without trailing newline
	msg2 := []byte("no newline")
	n2, err2 := w.Write(msg2)
	if err2 != nil {
		t.Fatalf("Writer.Write returned error: %v", err2)
	}
	if n2 != len(msg2) {
		t.Errorf("Writer.Write returned %d, want %d", n2, len(msg2))
	}
}

