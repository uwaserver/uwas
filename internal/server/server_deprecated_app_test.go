package server

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// type=app is deprecated: dispatchHandler answers it with a 502 naming the
// replacement. Validation still accepts it — removing it would stop an
// existing config from loading — so without a startup warning the operator
// only finds out when traffic arrives.
//
// The app: block goes with it. Nothing starts a process from a domain's own
// app config; merge.go copies the fields around, which is not the same as
// using them.

func startupLogs(t *testing.T, domains []config.Domain) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	// logger.New captures the writer at construction.
	log := logger.New("warn", "text")

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	cfg := &config.Config{
		Global:  config.GlobalConfig{WorkerCount: "1", LogLevel: "warn", LogFormat: "text"},
		Domains: domains,
	}
	s := New(cfg, log)
	s.cancel()

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func TestDeprecatedAppTypeWarnsAtStartup(t *testing.T) {
	out := startupLogs(t, []config.Domain{{
		Host: "app.test",
		Type: "app",
		SSL:  config.SSLConfig{Mode: "off"},
	}})

	if !strings.Contains(out, "no longer supported") {
		t.Errorf("type=app produced no warning — the operator only finds out when traffic arrives:\n%s", out)
	}
	if !strings.Contains(out, "apps://") {
		t.Errorf("the warning does not name the replacement:\n%s", out)
	}
	if !strings.Contains(out, "app.test") {
		t.Errorf("the warning does not say which domain:\n%s", out)
	}
}

// A leftover app: block on a supported type must be reported too: it looks
// like configuration and does nothing.
func TestIgnoredAppBlockWarns(t *testing.T) {
	out := startupLogs(t, []config.Domain{{
		Host: "proxy.test",
		Type: "proxy",
		SSL:  config.SSLConfig{Mode: "off"},
		App:  config.AppConfig{Command: "npm start", Runtime: "node"},
		Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}},
		},
	}})

	if !strings.Contains(out, "app: block is ignored") {
		t.Errorf("no warning for an ignored app block:\n%s", out)
	}
}

// A domain that mentions neither must stay quiet.
func TestNoAppConfigNoWarning(t *testing.T) {
	out := startupLogs(t, []config.Domain{{
		Host: "plain.test",
		Type: "static",
		Root: t.TempDir(),
		SSL:  config.SSLConfig{Mode: "off"},
	}})

	if strings.Contains(out, "app:") || strings.Contains(out, "no longer supported") {
		t.Errorf("warned about a domain with no app configuration:\n%s", out)
	}
}
