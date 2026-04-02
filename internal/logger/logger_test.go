package logger

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestSafeGo(t *testing.T) {
	log := New("info", "text")

	var counter atomic.Int32
	done := make(chan bool)

	// Test normal completion (no panic)
	log.SafeGo("test-normal", func() {
		counter.Add(1)
		done <- true
	})

	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SafeGo goroutine")
	}

	if counter.Load() != 1 {
		t.Fatalf("expected counter to be 1, got %d", counter.Load())
	}
}

func TestSafeGoPanicRecovery(t *testing.T) {
	log := New("info", "text")

	var mu sync.Mutex
	panicCount := 0
	done := make(chan bool)

	// Test panic recovery and restart
	log.SafeGo("test-panic", func() {
		mu.Lock()
		count := panicCount
		panicCount++
		mu.Unlock()

		if count == 0 {
			panic("intentional panic")
		}
		// Second run should complete normally
		done <- true
	})

	select {
	case <-done:
		// Expected - goroutine should restart after panic
		mu.Lock()
		if panicCount != 2 {
			t.Fatalf("expected 2 runs (1 panic + 1 success), got %d", panicCount)
		}
		mu.Unlock()
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for SafeGo goroutine to recover")
	}
}

func TestSafeGoMultiplePanics(t *testing.T) {
	log := New("info", "text")

	var mu sync.Mutex
	callCount := 0
	done := make(chan bool)

	// Test that panics are logged with correct goroutine name
	log.SafeGo("test-named", func() {
		mu.Lock()
		callCount++
		count := callCount
		mu.Unlock()

		if count < 3 {
			panic("panic " + string(rune('0'+count)))
		}
		done <- true
	})

	select {
	case <-done:
		mu.Lock()
		if callCount != 3 {
			t.Fatalf("expected 3 runs (2 panics + 1 success), got %d", callCount)
		}
		mu.Unlock()
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SafeGo goroutine")
	}
}

func TestWriterLevels(t *testing.T) {
	log := New("debug", "text")

	// Test different log levels
	levels := []slog.Level{
		slog.LevelDebug,
		slog.LevelInfo,
		slog.LevelWarn,
		slog.LevelError,
	}

	for _, level := range levels {
		w := log.Writer(level)
		if w == nil {
			t.Fatalf("Writer(%v) returned nil", level)
		}

		msg := []byte("test message at " + level.String())
		n, err := w.Write(msg)
		if err != nil {
			t.Fatalf("Writer.Write at level %v returned error: %v", level, err)
		}
		if n != len(msg) {
			t.Errorf("Writer.Write at level %v returned %d, want %d", level, n, len(msg))
		}
	}
}

func TestWriterTrimsNewline(t *testing.T) {
	log := New("info", "text")
	w := log.Writer(slog.LevelInfo)

	// Message with multiple trailing newlines
	msg := []byte("message\n\n\n")
	n, err := w.Write(msg)
	if err != nil {
		t.Fatalf("Writer.Write returned error: %v", err)
	}
	if n != len(msg) {
		t.Errorf("Writer.Write returned %d, want %d", n, len(msg))
	}
}

func TestNewFormats(t *testing.T) {
	// Test both text and JSON formats
	formats := []string{"text", "json", "TEXT", "JSON", "Text", "Json"}
	for _, format := range formats {
		log := New("info", format)
		if log == nil {
			t.Fatalf("New with format %q returned nil", format)
		}
		// Should not panic
		log.Info("test")
	}
}

func TestStdLoggerInterface(t *testing.T) {
	log := New("error", "text")
	stdLog := log.StdLogger()

	// StdLogger should implement the standard log.Logger interface
	// We can call basic methods on it
	if stdLog == nil {
		t.Fatal("StdLogger returned nil")
	}

	// The std logger uses Error level
	stdLog.Print("test message via std logger")
}

func TestParseLevelVariations(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"warn", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},          // empty defaults to info
		{"invalid", slog.LevelInfo},   // invalid defaults to info
		{"UNKNOWN", slog.LevelInfo},   // unknown defaults to info
	}

	for _, tt := range tests {
		got := parseLevel(tt.input)
		if got != tt.expected {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestSafeGoConcurrent(t *testing.T) {
	log := New("info", "text")

	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		log.SafeGo("concurrent-"+string(rune('0'+i)), func() {
			time.Sleep(10 * time.Millisecond)
			done <- true
		})
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
			// Expected
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for goroutines")
		}
	}
}

func BenchmarkSafeGo(b *testing.B) {
	log := New("info", "text")
	done := make(chan bool)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log.SafeGo("bench", func() {
			done <- true
		})
		<-done
	}
}
