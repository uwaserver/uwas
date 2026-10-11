package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// A replacement .htaccess that keeps its mtime (cp -p, rsync -t, tar) must
// still be re-parsed, or the old rules - including old access denies - keep
// applying until restart (F2260).
func TestHtaccessCacheDetectsSameMtimeReplacement(t *testing.T) {
	firstHeader := func(e *htaccessCacheEntry) string {
		if e == nil || e.raw == nil || len(e.raw.Headers) == 0 {
			return ""
		}
		return e.raw.Headers[0].Name
	}
	mt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	write := func(p, body string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	srv := func() *Server {
		return New(&config.Config{Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"}}, logger.New("error", "text"))
	}

	t.Run("size change with preserved mtime", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, ".htaccess")
		write(p, "Header set X-Old 1\n", mt)
		s := srv()
		if got := firstHeader(s.getHtaccessRuleSet(dir)); got != "X-Old" {
			t.Fatalf("first parse = %q", got)
		}
		write(p, "Header set X-Replaced-With-A-Longer-Name 1\n", mt)
		if got := firstHeader(s.getHtaccessRuleSet(dir)); got != "X-Replaced-With-A-Longer-Name" {
			t.Fatalf("stale rules after same-mtime replacement: %q", got)
		}
	})

	t.Run("mtime change still picked up", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, ".htaccess")
		write(p, "Header set X-Old 1\n", mt)
		s := srv()
		s.getHtaccessRuleSet(dir)
		write(p, "Header set X-New 1\n", mt.Add(time.Hour))
		if got := firstHeader(s.getHtaccessRuleSet(dir)); got != "X-New" {
			t.Fatalf("mtime change not picked up: %q", got)
		}
	})

	t.Run("unchanged file keeps its cached entry", func(t *testing.T) {
		dir := t.TempDir()
		write(filepath.Join(dir, ".htaccess"), "Header set X-Old 1\n", mt)
		s := srv()
		a := s.getHtaccessRuleSet(dir)
		if b := s.getHtaccessRuleSet(dir); a != b {
			t.Fatal("unchanged file was re-parsed")
		}
	})

	t.Run("unparsable file stays cached until it changes", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, ".htaccess")
		write(p, "<Files \"x\"\n", mt)
		s := srv()
		a := s.getHtaccessRuleSet(dir)
		if a == nil || !a.parseFailed {
			t.Skip("fixture is not unparsable in this parser version")
		}
		if b := s.getHtaccessRuleSet(dir); a != b {
			t.Fatal("unchanged broken file was re-parsed")
		}
		write(p, "Header set X-Fixed 1\n", mt)
		if got := firstHeader(s.getHtaccessRuleSet(dir)); got != "X-Fixed" {
			t.Fatalf("fixed same-mtime file not picked up: %q", got)
		}
	})
}
