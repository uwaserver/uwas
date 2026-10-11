package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2860: the Alerter was built from the startup global.alerting settings and
// never refreshed, so switching alerting on or off (or changing a destination)
// and reloading had no effect until restart.

func alertingReloadConfig(t *testing.T, dir string, enabled bool) string {
	t.Helper()
	root := filepath.Join(dir, "site")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	y := fmt.Sprintf(`global:
  worker_count: "1"
  log_level: error
  log_format: text
  alerting:
    enabled: %v
domains:
  - host: al.test
    type: static
    root: %s
    ssl:
      mode: "off"
`, enabled, root)
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func alertRecorded(s *Server) int {
	s.alerter.Alert(alerting.Alert{Level: "warning", Type: "domain_down", Host: "al.test", Message: "x"})
	return len(s.alerter.Alerts())
}

func TestReloadRefreshesAlerting(t *testing.T) {
	dir := t.TempDir()
	path := alertingReloadConfig(t, dir, true)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)

	if got := alertRecorded(s); got != 1 {
		t.Fatalf("control: enabled alerter recorded %d, want 1", got)
	}

	alertingReloadConfig(t, dir, false)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := alertRecorded(s); got != 1 {
		t.Errorf("alerting still recording after being disabled by reload: %d alerts, want 1", got)
	}

	alertingReloadConfig(t, dir, true)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := alertRecorded(s); got != 2 {
		t.Errorf("alerting not recording after being re-enabled by reload: %d alerts, want 2", got)
	}
}

func TestReloadEnablesAlertingStartedDisabled(t *testing.T) {
	dir := t.TempDir()
	path := alertingReloadConfig(t, dir, false)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)

	if got := alertRecorded(s); got != 0 {
		t.Fatalf("control: disabled alerter recorded %d, want 0", got)
	}
	alertingReloadConfig(t, dir, true)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := alertRecorded(s); got != 1 {
		t.Errorf("alerting not enabled by reload: %d alerts, want 1", got)
	}
}
