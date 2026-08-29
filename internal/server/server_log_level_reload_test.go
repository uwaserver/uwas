package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// global.log_level was read once at startup and never again. A reload accepted
// a new value, wrote it to the config and showed it in the panel while the
// process kept logging at the old threshold — the operator had to restart to
// quieten a noisy server, which is the moment they least want to.

func writeReloadConfig(t *testing.T, level string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "uwas.yaml")
	yaml := "global:\n  log_level: " + level + "\n  log_format: text\n  worker_count: \"1\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestReloadAppliesLogLevel(t *testing.T) {
	s := newDispatchTestServer(t, nil)
	s.logger.SetLevel("info")

	s.configPath = writeReloadConfig(t, "warn")
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := s.logger.Level(); got != slog.LevelWarn {
		t.Errorf("level after reload = %v, want warn — the new value did not reach the logger", got)
	}
	if s.logger.Enabled(context.TODO(), slog.LevelInfo) {
		t.Error("info is still enabled after reloading with log_level: warn")
	}
}

// It has to move both ways: turning debug on mid-incident must not need a
// restart either.
func TestReloadLowersLogLevel(t *testing.T) {
	s := newDispatchTestServer(t, nil)
	s.logger.SetLevel("error")

	s.configPath = writeReloadConfig(t, "debug")
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if !s.logger.Enabled(context.TODO(), slog.LevelDebug) {
		t.Error("debug is not enabled after reloading with log_level: debug")
	}
}

// The global access-log switch is a pointer so an absent block keeps the
// request stream on, which is what has always happened.
func TestGlobalAccessLogDefaultsOn(t *testing.T) {
	if !(config.GlobalAccessLog{}).RequestLogEnabled() {
		t.Error("an absent access_log block turned the request log off")
	}
	if !(config.GlobalAccessLog{Enabled: config.BoolPtr(true)}).RequestLogEnabled() {
		t.Error("an explicit true turned it off")
	}
	if (config.GlobalAccessLog{Enabled: config.BoolPtr(false)}).RequestLogEnabled() {
		t.Error("an explicit false did not turn it off")
	}
}

// access_log.enabled had the same shape of problem as log_level, one layer
// further out: the middleware chain is compiled once in New and never rebuilt,
// so a reload could accept the new value and the running chain would keep
// writing the line. The panel exposes this as a toggle, so it has to take
// effect on Save & Reload rather than on the next restart.

func writeAccessLogConfig(t *testing.T, enabled bool) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "uwas.yaml")
	yaml := "global:\n  log_level: info\n  log_format: text\n  worker_count: \"1\"\n  access_log:\n    enabled: " +
		map[bool]string{true: "true", false: "false"}[enabled] + "\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestReloadAppliesAccessLogToggle(t *testing.T) {
	s := newDispatchTestServer(t, nil)
	if !s.requestLog.Load() {
		t.Fatal("the request log started off — an absent access_log block means on")
	}

	s.configPath = writeAccessLogConfig(t, false)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s.requestLog.Load() {
		t.Error("access_log.enabled: false did not reach the running server")
	}

	// And back, so an operator who quietened a server during an incident can
	// turn the stream on again without a restart.
	s.configPath = writeAccessLogConfig(t, true)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !s.requestLog.Load() {
		t.Error("access_log.enabled: true did not turn the request log back on")
	}
}

// The flag reaching the Server is not the point — the chain built at startup
// has to stop writing. This drives a request through that same chain before
// and after the reload.
func TestReloadAccessLogToggleAffectsBuiltChain(t *testing.T) {
	s := newDispatchTestServer(t, nil)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	// The logger binds its writer at construction, so it and the chain that
	// captures it are both built after the swap.
	s.logger = logger.New("info", "text")
	chain := s.buildMiddlewareChain() // as Start does, once

	serve := func(path string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "absent.test"
		req.Header.Set("User-Agent", "uwas-test")
		chain.ServeHTTP(httptest.NewRecorder(), req)
	}

	serve("/before-reload")

	s.configPath = writeAccessLogConfig(t, false)
	if err := s.reload(); err != nil {
		os.Stdout = orig
		_ = w.Close()
		t.Fatalf("reload: %v", err)
	}
	serve("/after-reload")

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()

	if !strings.Contains(out, "/before-reload") {
		t.Errorf("the request before the reload was not logged:\n%s", out)
	}
	if strings.Contains(out, "/after-reload") {
		t.Errorf("the chain kept logging after the reload turned the request log off:\n%s", out)
	}
}
