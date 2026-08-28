package admin

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// admin.oauth is stored, merged and returned by the settings API, and no
// OAuth login flow exists. An operator who enables it and fills in
// allowed_emails could reasonably believe the panel is limited to those
// addresses. Nothing enforces that, so the server says so at startup.
//
// The logger writes to stdout with no injectable writer, so the test captures
// stdout around New(). Tests in this package do not run in parallel, so the
// swap is safe.

func captureStdout(t *testing.T, kur func(log *logger.Logger)) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	// logger.New captures the writer at construction, so it must be built
	// after the swap.
	log := logger.New("warn", "text")

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	kur(log)

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func oauthConfig(enabled bool) *config.Config {
	cfg := &config.Config{Global: config.GlobalConfig{
		Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
	}}
	if enabled {
		cfg.Global.Admin.OAuth = config.OAuthConfig{
			Enabled:       true,
			AllowedEmails: []string{"admin@example.com"},
		}
	}
	return cfg
}

func TestOAuthEnabledWarnsItIsNotImplemented(t *testing.T) {
	cfg := oauthConfig(true)
	out := captureStdout(t, func(log *logger.Logger) {
		New(cfg, log, metrics.New())
	})

	if !strings.Contains(out, "not implemented") {
		t.Errorf("oauth.enabled produced no warning — the operator would believe the panel is restricted:\n%s", out)
	}
	if !strings.Contains(out, "allowed_emails") {
		t.Errorf("the warning does not mention allowed_emails — that is where the false belief lives:\n%s", out)
	}
}

func TestOAuthDisabledDoesNotWarn(t *testing.T) {
	cfg := oauthConfig(false)
	out := captureStdout(t, func(log *logger.Logger) {
		New(cfg, log, metrics.New())
	})

	if strings.Contains(strings.ToLower(out), "oauth") {
		t.Errorf("warned with oauth disabled:\n%s", out)
	}
}
