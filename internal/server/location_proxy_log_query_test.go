package server

import (
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// Location proxy failure logs must carry the cause but never the visitor's
// query string (F2110).
func TestLocationProxyLogsOmitQuery(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	ln.Close()

	cases := []struct {
		name     string
		upstream string // "" = the closed loopback port; metadata IP is always blocked
		target   string // request target
		wantMsg  string
		wantErr  string // substring that proves the cause is still logged
	}{
		{"dial failure", "", "/api/x?token=SECRETVALUE123&a=1", "location proxy error", "connection refused"},
		{"dial failure, no query", "", "/api/x", "location proxy error", "connection refused"},
		{"ssrf blocked", "http://169.254.169.254", "/api/x?token=SECRETVALUE123", "location proxy SSRF blocked", ""},
		{"ssrf blocked, no query", "http://169.254.169.254", "/api/x", "location proxy SSRF blocked", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := dead
			if tc.upstream != "" {
				up = tc.upstream
			}
			cfg := &config.Config{
				Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
				Domains: []config.Domain{{
					Host: "loc.test", Type: "static", Root: t.TempDir(),
					SSL:       config.SSLConfig{Mode: "off"},
					Proxy:     config.ProxyConfig{AllowPrivateUpstreams: true},
					Locations: []config.LocationConfig{{Match: "/api/", ProxyPass: up}},
				}},
			}
			s := New(cfg, logger.New("error", "text"))
			t.Cleanup(func() { s.cancel() })
			var buf bytes.Buffer
			s.logger.Logger = slog.New(slog.NewTextHandler(&buf, nil))
			h := s.buildMiddlewareChain()

			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.Host = "loc.test"
			req.Header.Set("User-Agent", "uwas-test")
			h.ServeHTTP(httptest.NewRecorder(), req)

			var line string
			for _, l := range strings.Split(buf.String(), "\n") {
				if strings.Contains(l, tc.wantMsg) {
					line = l
				}
			}
			if line == "" {
				t.Fatalf("no %q log line in %q", tc.wantMsg, buf.String())
			}
			if strings.Contains(line, "SECRETVALUE123") || strings.Contains(line, "token=") {
				t.Errorf("log line leaks the query string: %s", line)
			}
			if tc.wantErr != "" && !strings.Contains(line, tc.wantErr) {
				t.Errorf("log line lost the failure cause (%q): %s", tc.wantErr, line)
			}
		})
	}
}
