package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

func TestDomainLogWrite(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	m := newDomainLogManager()
	defer m.Close()

	m.Write("example.com", logPath, config.RotateConfig{},
		"GET", "/index.html", "127.0.0.1", "TestAgent",
		200, 1024, 5*time.Millisecond)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "127.0.0.1") {
		t.Error("expected remote IP in log")
	}
	if !strings.Contains(content, "GET /index.html") {
		t.Error("expected request line in log")
	}
	if !strings.Contains(content, "200") {
		t.Error("expected status code in log")
	}
	if !strings.Contains(content, "TestAgent") {
		t.Error("expected user agent in log")
	}
}

func TestDomainLogRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	m := newDomainLogManager()
	defer m.Close()

	// Use tiny max size to trigger rotation
	rotate := config.RotateConfig{
		MaxSize:    config.ByteSize(200),
		MaxBackups: 3,
	}

	// Write enough to trigger rotation
	for i := 0; i < 10; i++ {
		m.Write("example.com", logPath, rotate,
			"GET", "/page", "127.0.0.1", "Agent",
			200, 100, time.Millisecond)
	}

	// Wait for background compression
	time.Sleep(500 * time.Millisecond)

	// Check that the active log exists
	if _, err := os.Stat(logPath); err != nil {
		t.Error("expected active log file to exist after rotation")
	}

	// Check that rotated files exist
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var rotated int
	for _, e := range entries {
		if e.Name() != "access.log" && strings.HasPrefix(e.Name(), "access.log.") {
			rotated++
		}
	}
	if rotated == 0 {
		t.Error("expected at least 1 rotated file")
	}
}

func TestDomainLogMultipleDomains(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.log")
	pathB := filepath.Join(dir, "b.log")

	m := newDomainLogManager()
	defer m.Close()

	m.Write("a.com", pathA, config.RotateConfig{},
		"GET", "/a", "10.0.0.1", "A", 200, 50, time.Millisecond)
	m.Write("b.com", pathB, config.RotateConfig{},
		"POST", "/b", "10.0.0.2", "B", 201, 75, time.Millisecond)

	dataA, _ := os.ReadFile(pathA)
	dataB, _ := os.ReadFile(pathB)

	if !strings.Contains(string(dataA), "10.0.0.1") {
		t.Error("domain A log should contain its own IP")
	}
	if !strings.Contains(string(dataB), "10.0.0.2") {
		t.Error("domain B log should contain its own IP")
	}
}

func TestDomainLogEmptyPath(t *testing.T) {
	m := newDomainLogManager()
	defer m.Close()

	// Should not panic with empty path
	m.Write("example.com", "", config.RotateConfig{},
		"GET", "/", "127.0.0.1", "Agent", 200, 0, time.Millisecond)
}

func TestFindRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	// Create some rotated files
	os.WriteFile(base, []byte("active"), 0644)
	os.WriteFile(base+".20260101-120000.gz", []byte("old1"), 0644)
	os.WriteFile(base+".20260102-120000.gz", []byte("old2"), 0644)

	rotated := findRotatedFiles(base)
	if len(rotated) != 2 {
		t.Errorf("expected 2 rotated files, got %d", len(rotated))
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	// Create 5 rotated files
	for i := 1; i <= 5; i++ {
		name := filepath.Join(dir, "access.log.2026010"+string(rune('0'+i))+"-120000.gz")
		os.WriteFile(name, []byte("data"), 0644)
	}

	pruneBackups(base, 2)

	rotated := findRotatedFiles(base)
	if len(rotated) != 2 {
		t.Errorf("expected 2 rotated files after prune, got %d", len(rotated))
	}
}
