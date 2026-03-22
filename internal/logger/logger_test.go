package logger

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// ========== AccessLogger tests (accesslog.go) ==========

func TestNewAccessLoggerEmptyPath(t *testing.T) {
	al, err := NewAccessLogger(AccessLogConfig{Path: ""})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if al != nil {
		t.Error("empty path should return nil logger")
	}
}

func TestNewAccessLoggerValidPath(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "json",
		MaxSize:    0,
		MaxBackups: 3,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if al == nil {
		t.Fatal("expected non-nil logger")
	}
	defer al.Close()

	// File should exist
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("log file should be created")
	}
}

func TestNewAccessLoggerDefaultFormat(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path: logPath,
		// Format and MaxBackups left empty to test defaults
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if al == nil {
		t.Fatal("expected non-nil logger")
	}
	defer al.Close()

	if al.format != "json" {
		t.Errorf("default format = %q, want json", al.format)
	}
	if al.maxBackups != 5 {
		t.Errorf("default maxBackups = %d, want 5", al.maxBackups)
	}
}

func TestAccessLoggerLogJSON(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	al.Log("GET", "example.com", "/index.html", "1.2.3.4", "TestAgent/1.0", "req-123",
		200, 1024, 15, 5)

	// Close and read the file
	al.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `"method":"GET"`) {
		t.Errorf("log should contain method, got:\n%s", content)
	}
	if !strings.Contains(content, `"host":"example.com"`) {
		t.Errorf("log should contain host, got:\n%s", content)
	}
	if !strings.Contains(content, `"path":"/index.html"`) {
		t.Errorf("log should contain path, got:\n%s", content)
	}
	if !strings.Contains(content, `"status":200`) {
		t.Errorf("log should contain status, got:\n%s", content)
	}
	if !strings.Contains(content, `"request_id":"req-123"`) {
		t.Errorf("log should contain request_id, got:\n%s", content)
	}
}

func TestAccessLoggerLogCLF(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "clf",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	al.Log("POST", "example.com", "/submit", "10.0.0.1", "Mozilla/5.0", "req-456",
		201, 512, 20, 3)

	al.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	content := string(data)
	// CLF format: IP - - [date] "METHOD PATH HTTP/1.1" STATUS BYTES "-" "USER_AGENT"
	if !strings.Contains(content, "10.0.0.1") {
		t.Errorf("CLF should contain remote IP, got:\n%s", content)
	}
	if !strings.Contains(content, `"POST /submit HTTP/1.1"`) {
		t.Errorf("CLF should contain request line, got:\n%s", content)
	}
	if !strings.Contains(content, "201") {
		t.Errorf("CLF should contain status, got:\n%s", content)
	}
	if !strings.Contains(content, "Mozilla/5.0") {
		t.Errorf("CLF should contain user agent, got:\n%s", content)
	}
}

func TestAccessLoggerRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "json",
		MaxSize:    100, // very small to trigger rotation quickly
		MaxBackups: 3,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write enough lines to trigger rotation
	for i := 0; i < 20; i++ {
		al.Log("GET", "test.com", "/page", "1.2.3.4", "Agent", "id",
			200, 100, 10, 2)
	}

	al.Close()

	// Check that backup files were created
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	backupCount := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "access.log.") {
			backupCount++
		}
	}

	if backupCount == 0 {
		t.Error("rotation should have created backup files")
	}

	// The current access.log should exist and be smaller than maxSize
	// (or just exist since we wrote past maxSize multiple times)
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("access.log should still exist after rotation")
	}
}

func TestAccessLoggerCleanOldBackups(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	// Create fake backup files (more than maxBackups)
	for i := 0; i < 8; i++ {
		name := filepath.Join(dir, "access.log.2025010"+string(rune('0'+i))+"-120000")
		os.WriteFile(name, []byte("old log"), 0644)
	}

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "json",
		MaxSize:    50, // very small
		MaxBackups: 3,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write enough to trigger rotation which calls cleanOldBackups
	for i := 0; i < 10; i++ {
		al.Log("GET", "test.com", "/", "1.1.1.1", "A", "r",
			200, 50, 1, 1)
	}

	al.Close()

	// Count backup files — should be limited to maxBackups
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	backupCount := 0
	for _, e := range entries {
		if e.Name() != "access.log" && strings.HasPrefix(e.Name(), "access.log.") {
			backupCount++
		}
	}

	if backupCount > 3 {
		t.Errorf("should have at most 3 backups, got %d", backupCount)
	}
}

func TestAccessLoggerReopen(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write a line
	al.Log("GET", "test.com", "/before", "1.1.1.1", "A", "r1",
		200, 50, 1, 1)

	// Reopen the file
	err = al.Reopen()
	if err != nil {
		t.Fatalf("Reopen error: %v", err)
	}

	// Write another line after reopen
	al.Log("GET", "test.com", "/after", "1.1.1.1", "A", "r2",
		200, 50, 1, 1)

	al.Close()

	// Both lines should be in the file
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "/before") {
		t.Error("should contain entry from before reopen")
	}
	if !strings.Contains(content, "/after") {
		t.Error("should contain entry from after reopen")
	}
}

func TestAccessLoggerClose(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Close should not error
	err = al.Close()
	if err != nil {
		t.Errorf("Close error: %v", err)
	}

	// Close on nil should not error
	var nilLogger *AccessLogger
	err = nilLogger.Close()
	if err != nil {
		t.Errorf("nil Close error: %v", err)
	}
}

func TestNewAccessLoggerInvalidPath(t *testing.T) {
	// Try to create in a path that won't work (file as directory)
	dir := t.TempDir()
	blockFile := filepath.Join(dir, "blocker")
	os.WriteFile(blockFile, []byte("x"), 0644)

	// Use the regular file as parent dir — should fail
	badPath := filepath.Join(blockFile, "subdir", "access.log")
	al, err := NewAccessLogger(AccessLogConfig{Path: badPath})
	if err == nil {
		if al != nil {
			al.Close()
		}
		t.Fatal("expected error for invalid path")
	}
}

func TestAccessLoggerWrittenTracker(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	// Pre-create a file with some content to test the initial written tracking
	os.WriteFile(logPath, []byte("pre-existing content\n"), 0644)

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// The written counter should reflect the pre-existing file size
	if al.written == 0 {
		t.Error("written should reflect pre-existing file size")
	}
}

// --- accesslog.go: Reopen resets written counter ---

func TestAccessLoggerReopenResetsWritten(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write some data
	al.Log("GET", "test.com", "/page", "1.1.1.1", "A", "r1", 200, 50, 1, 1)

	if al.written == 0 {
		t.Fatal("written should be > 0 after logging")
	}

	// Reopen should reset written to 0
	err = al.Reopen()
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	if al.written != 0 {
		t.Errorf("written = %d after Reopen, want 0", al.written)
	}
}

// --- accesslog.go: Reopen with file that was deleted between close and reopen ---

func TestAccessLoggerReopenDeletedFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Delete the file before reopening (simulating log rotation by external tool)
	os.Remove(logPath)

	// Reopen should recreate the file
	err = al.Reopen()
	if err != nil {
		t.Fatalf("Reopen after delete: %v", err)
	}

	// Should be able to write after reopen
	al.Log("GET", "test.com", "/after", "1.1.1.1", "A", "r2", 200, 50, 1, 1)

	al.Close()

	// File should exist again
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("log file should be recreated after Reopen")
	}
}

// --- accesslog.go: rotation creates new file and resets written ---

func TestAccessLoggerRotationResetWritten(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "clf",
		MaxSize:    50,
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write enough to trigger at least one rotation
	for i := 0; i < 10; i++ {
		al.Log("GET", "test.com", "/p", "1.1.1.1", "A", "r", 200, 100, 1, 1)
	}

	// After rotation, written should be less than total
	if al.written > 500 {
		t.Errorf("written = %d, should be < 500 after rotation", al.written)
	}
}

// --- accesslog.go: Close on already-closed logger ---

func TestAccessLoggerDoubleClose(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// First close
	al.Close()
	// Second close - file handle is invalid but shouldn't panic
	// (it may error, which is fine)
	al.Close()
}

// --- accesslog.go: cleanOldBackups with no backups ---

func TestAccessLoggerCleanOldBackupsNoBackups(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "json",
		MaxSize:    10000, // large enough that we don't rotate
		MaxBackups: 3,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Directly call cleanOldBackups with no backups - should not panic
	al.cleanOldBackups()
}

// --- accesslog.go: NewAccessLogger with negative maxBackups defaults to 5 ---

// --- accesslog.go: NewAccessLogger OpenFile error (dir exists but can't open as file) ---

func TestNewAccessLoggerOpenFileError(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory at the log file path — can't open a directory as a file
	logPath := filepath.Join(dir, "access.log")
	os.MkdirAll(logPath, 0755) // create the path as a directory

	_, err := NewAccessLogger(AccessLogConfig{Path: logPath})
	if err == nil {
		t.Fatal("expected error when log path is a directory")
	}
}

func TestAccessLoggerNegativeMaxBackups(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "json",
		MaxBackups: -1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	if al.maxBackups != 5 {
		t.Errorf("maxBackups = %d, want 5 for negative input", al.maxBackups)
	}
}

// --- accesslog.go: Reopen with invalid path ---

func TestAccessLoggerReopenInvalidPath(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Change the path to something invalid
	al.path = filepath.Join(dir, "nonexistent_subdir", "deeply", "nested", "access.log")

	// Reopen should fail because the parent directory doesn't exist
	err = al.Reopen()
	if err == nil {
		t.Fatal("expected error for Reopen with invalid path")
	}
}

// --- accesslog.go: Reopen with nil file handle ---

func TestAccessLoggerReopenNilFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:   logPath,
		Format: "json",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Set file to nil to test the nil check path
	al.file.Close()
	al.file = nil

	// Reopen should handle nil file gracefully
	err = al.Reopen()
	if err != nil {
		t.Fatalf("Reopen with nil file: %v", err)
	}

	// Should be able to write after reopen
	al.Log("GET", "test.com", "/after-nil", "1.1.1.1", "A", "r", 200, 50, 1, 1)
	al.Close()
}

// --- accesslog.go: rotate when directory is unreadable for cleanOldBackups ---

func TestAccessLoggerRotateCleanupDirError(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "json",
		MaxSize:    50,
		MaxBackups: 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write enough to trigger rotation
	for i := 0; i < 5; i++ {
		al.Log("GET", "test.com", "/p", "1.1.1.1", "A", "r", 200, 100, 1, 1)
	}
	// Just ensuring rotation doesn't panic
}

// --- accesslog.go: rotate with invalid path (OpenFile fails) ---

func TestAccessLoggerRotateInvalidPath(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "json",
		MaxSize:    50,
		MaxBackups: 1,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write enough to trigger first rotation
	al.Log("GET", "test.com", "/p1234567890", "1.1.1.1", "Agent/1.0", "rid", 200, 100, 1, 1)

	// Now change path to something that can't be opened
	al.path = filepath.Join(dir, "no_such_dir", "access.log")

	// Next write should trigger rotation with invalid path
	al.Log("GET", "test.com", "/p1234567890", "1.1.1.1", "Agent/1.0", "rid", 200, 100, 1, 1)
	al.Log("GET", "test.com", "/p1234567890", "1.1.1.1", "Agent/1.0", "rid", 200, 100, 1, 1)

	// Should not panic even though rotate fails
}

// --- accesslog.go: multiple rotations in sequence ---

func TestAccessLoggerMultipleRotations(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	al, err := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		Format:     "clf",
		MaxSize:    100,
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer al.Close()

	// Write enough to trigger many rotations
	for i := 0; i < 50; i++ {
		al.Log("GET", "test.com", "/page", "1.2.3.4", "Agent/1.0", "rid",
			200, 1024, 10, 2)
	}

	al.Close()

	// Count backups - should be limited to maxBackups
	entries, _ := os.ReadDir(dir)
	backupCount := 0
	for _, e := range entries {
		name := e.Name()
		if name != "access.log" && strings.HasPrefix(name, "access.log.") {
			backupCount++
		}
	}

	if backupCount > 2 {
		t.Errorf("should have at most 2 backups, got %d", backupCount)
	}
}
