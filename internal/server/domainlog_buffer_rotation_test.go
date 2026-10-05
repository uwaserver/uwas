package server

import (
	"github.com/uwaserver/uwas/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bufferedRotationAuditRotation(t *testing.T, buffer int) (string, *domainLogManager) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "access.log")
	m := newDomainLogManager()
	t.Cleanup(m.Close)
	cfg := config.AccessLogConfig{Path: path, BufferSize: buffer, Rotate: config.RotateConfig{MaxSize: 500, MaxBackups: 10}}
	m.Write("log.test", cfg, "GET", "/"+strings.Repeat("first", 150), "192.0.2.1", "agent", 200, 1, time.Millisecond)
	m.bg.Wait()
	m.Write("log.test", cfg, "GET", "/after-rotation", "192.0.2.1", "agent", 200, 1, time.Millisecond)
	m.flushAll()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), m
}
func TestBufferedDomainLogFollowsRotation(t *testing.T) {
	for _, size := range []int{0, 128, 4096} {
		t.Run("rotation buffer", func(t *testing.T) {
			got, _ := bufferedRotationAuditRotation(t, size)
			if !strings.Contains(got, "/after-rotation") {
				t.Fatalf("buffer=%d post-rotation line lost", size)
			}
		})
	}
	t.Run("gated next write after reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "access.log")
		m := newDomainLogManager()
		t.Cleanup(m.Close)
		cfg := config.AccessLogConfig{Path: path, BufferSize: 4096, Rotate: config.RotateConfig{MaxSize: 1 << 20, MaxBackups: 10}}
		m.Write("log.test", cfg, "GET", "/before", "192.0.2.1", "agent", 200, 1, 0)
		d := m.files["log.test"]
		d.mu.Lock()
		ready := make(chan struct{})
		done := make(chan struct{})
		go func() {
			close(ready)
			m.Write("log.test", cfg, "GET", "/gated-after", "192.0.2.1", "agent", 200, 1, 0)
			close(done)
		}()
		<-ready
		m.rotateLocked("log.test", d)
		d.mu.Unlock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("write did not finish")
		}
		m.flushAll()
		m.bg.Wait()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "/gated-after") || strings.Contains(string(data), "/before") {
			t.Fatalf("wrong active generation %q", data)
		}
	})
	t.Run("unrotated buffered flush", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "access.log")
		m := newDomainLogManager()
		cfg := config.AccessLogConfig{Path: path, BufferSize: 4096}
		m.Write("log.test", cfg, "GET", "/normal", "192.0.2.1", "agent", 200, 1, 0)
		m.Close()
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), "/normal") {
			t.Fatalf("ordinary close lost line %q %v", data, err)
		}
	})
}
