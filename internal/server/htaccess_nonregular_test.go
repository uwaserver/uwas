//go:build unix

package server

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F1451: a FIFO (or an oversized file) planted as .htaccess must fail closed
// instead of hanging every request on open(2) or being read unbounded.
func TestParseHtaccessFullNonRegularFailsClosed(t *testing.T) {
	cfg := &config.Config{Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"}}
	s := New(cfg, logger.New("error", "text"))
	parse := func(root string) *htaccessCacheEntry {
		ch := make(chan *htaccessCacheEntry, 1)
		go func() { ch <- s.parseHtaccessFull(root) }()
		select {
		case e := <-ch:
			return e
		case <-time.After(5 * time.Second):
			t.Fatal("parseHtaccessFull blocked")
			return nil
		}
	}

	good := t.TempDir()
	os.WriteFile(filepath.Join(good, ".htaccess"), []byte("Header set X-A 1\n"), 0o644)
	if e := parse(good); e.raw == nil || e.parseFailed {
		t.Fatalf("control: regular .htaccess not parsed: %+v", e)
	}

	fifo := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(fifo, ".htaccess"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if e := parse(fifo); !e.parseFailed {
		t.Error("FIFO .htaccess: want parseFailed")
	}

	big := t.TempDir()
	f, err := os.Create(filepath.Join(big, ".htaccess"))
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(maxHtaccessSize + 1)
	f.Close()
	if e := parse(big); !e.parseFailed {
		t.Error("oversized .htaccess: want parseFailed")
	}
}
