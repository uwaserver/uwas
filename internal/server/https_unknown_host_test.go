package server

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2561 (regression): the HTTP listener answers a Host that matches no configured domain
// with 421 and counts it (unknown-host tracker / blocklist). The HTTPS entry
// handler routed the same request to the router's fallback domain (the first
// configured one), so a client holding a valid SNI could read the first
// tenant's site under any Host and never reached the tracker.
func TestHTTPSEntryRejectsUnknownHost(t *testing.T) {
	mk := func(body string) string {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, "index.html"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text", HTTPSListen: "127.0.0.1:0"},
		Domains: []config.Domain{
			{Host: "first.example.com", Root: mk("FIRST"), Type: "static", SSL: config.SSLConfig{Mode: "manual"}},
			{Host: "second.example.com", Aliases: []string{"alias.example.org"}, Root: mk("SECOND"), Type: "static", SSL: config.SSLConfig{Mode: "manual"}},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	if err := s.startHTTPS(); err != nil {
		t.Fatal(err)
	}
	defer s.httpsSrv.Close()

	get := func(host string) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "https://first.example.com/index.html", nil)
		req.Host = host
		req.Header.Set("User-Agent", "Mozilla/5.0 (test)")
		s.httpsSrv.Handler.ServeHTTP(rec, req)
		b, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(b)
	}

	// Configured spellings keep working: exact, case, trailing dot, port, alias.
	for host, want := range map[string]string{
		"second.example.com": "SECOND", "SECOND.example.com": "SECOND", "second.example.com.": "SECOND",
		"alias.example.org": "SECOND", "first.example.com": "FIRST",
	} {
		if code, body := get(host); code != 200 || body != want {
			t.Errorf("Host %q: got %d %q, want 200 %q", host, code, body, want)
		}
	}
	// Anything else is refused like on the HTTP listener and is counted.
	for _, host := range []string{"unknown.attacker.test", "10.1.2.3", ""} {
		if code, body := get(host); code != 421 || body == "FIRST" {
			t.Errorf("unknown Host %q: got %d %q, want 421", host, code, body)
		}
	}
	if n := len(s.unknownHosts.List()); n == 0 {
		t.Error("unknown hosts reached no tracker")
	}
	// A blocked unknown host is refused outright.
	s.unknownHosts.Block("blocked.attacker.test")
	if code, _ := get("blocked.attacker.test"); code != 403 {
		t.Errorf("blocked unknown Host: got %d, want 403", code)
	}
}
