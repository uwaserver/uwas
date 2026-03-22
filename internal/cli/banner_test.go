package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestPrintBannerDoesNotPanicEmpty(t *testing.T) {
	cfg := &config.Config{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Should not panic with empty config.
	PrintBanner(cfg, "/tmp/test.yaml")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Unified Web Application Server") {
		t.Error("banner should contain server name")
	}
	if !strings.Contains(output, "/tmp/test.yaml") {
		t.Error("banner should contain config path")
	}
}

func TestPrintBannerFullFeatures(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPListen:  ":8080",
			HTTPSListen: ":8443",
			HTTP3Enabled: true,
			Admin: config.AdminConfig{
				Enabled: true,
				Listen:  ":9443",
			},
			MCP: config.MCPConfig{
				Enabled: true,
				Listen:  ":9444",
			},
			Cache: config.CacheConfig{
				Enabled: true,
			},
			Backup: config.BackupConfig{
				Enabled: true,
			},
			Alerting: config.AlertingConfig{
				Enabled: true,
			},
		},
		Domains: []config.Domain{
			{Host: "example.com", Type: "static"},
			{Host: "api.example.com", Type: "proxy"},
			{Host: "old.example.com", Type: "redirect"},
			{Host: "php.example.com", Type: "php"},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(cfg, "/etc/uwas/uwas.yaml")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Check listeners.
	if !strings.Contains(output, ":8080") {
		t.Error("should show HTTP listen")
	}
	if !strings.Contains(output, ":8443") {
		t.Error("should show HTTPS listen")
	}
	if !strings.Contains(output, "HTTPS+H3") {
		t.Error("should show HTTP/3 label")
	}
	if !strings.Contains(output, "Admin") {
		t.Error("should show admin listener")
	}
	if !strings.Contains(output, "MCP") {
		t.Error("should show MCP listener")
	}

	// Check features.
	if !strings.Contains(output, "Cache") {
		t.Error("should show Cache feature")
	}
	if !strings.Contains(output, "Backup") {
		t.Error("should show Backup feature")
	}
	if !strings.Contains(output, "HTTP/3") {
		t.Error("should show HTTP/3 feature")
	}
	if !strings.Contains(output, "Alerting") {
		t.Error("should show Alerting feature")
	}

	// Check domain type counts.
	if !strings.Contains(output, "1 static") {
		t.Error("should show static domain count")
	}
	if !strings.Contains(output, "1 proxy") {
		t.Error("should show proxy domain count")
	}
	if !strings.Contains(output, "1 redirect") {
		t.Error("should show redirect domain count")
	}
	if !strings.Contains(output, "1 php") {
		t.Error("should show php domain count")
	}

	// Check dashboard URL.
	if !strings.Contains(output, "Dashboard") {
		t.Error("should show dashboard URL")
	}
	if !strings.Contains(output, "/_uwas/dashboard/") {
		t.Error("should contain dashboard path")
	}
}

func TestPrintBannerNoAdmin(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPListen: ":80",
			Admin: config.AdminConfig{
				Enabled: false,
			},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(cfg, "uwas.yaml")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if strings.Contains(output, "Dashboard") {
		t.Error("should not show dashboard when admin is disabled")
	}
	if strings.Contains(output, "Admin") {
		t.Error("should not show admin listener when disabled")
	}
}

func TestPrintBannerHTTPSWithoutH3(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPListen:  ":80",
			HTTPSListen: ":443",
			HTTP3Enabled: false,
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(cfg, "uwas.yaml")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should show "HTTPS" but not "HTTPS+H3".
	if strings.Contains(output, "HTTPS+H3") {
		t.Error("should not show H3 when HTTP3 is disabled")
	}
	if !strings.Contains(output, "HTTPS") {
		t.Error("should show HTTPS listener")
	}
}

func TestPrintBannerNoFeatures(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPListen: ":80",
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(cfg, "uwas.yaml")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// No features should mean no "Features:" line.
	if strings.Contains(output, "Features:") {
		t.Error("should not show Features line when nothing is enabled")
	}
}

func TestPrintBannerAdminWithColonPrefix(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPListen: ":80",
			Admin: config.AdminConfig{
				Enabled: true,
				Listen:  ":9443",
			},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(cfg, "uwas.yaml")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// When admin listen starts with ":", it should prepend "localhost".
	if !strings.Contains(output, "localhost:9443") {
		t.Error("should prepend localhost to :port")
	}
}

func TestPrintBannerAdminFullAddress(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPListen: ":80",
			Admin: config.AdminConfig{
				Enabled: true,
				Listen:  "192.168.1.1:9443",
			},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintBanner(cfg, "uwas.yaml")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "192.168.1.1:9443") {
		t.Error("should show full address without localhost prefix")
	}
}

func TestColorize(t *testing.T) {
	tests := []struct {
		input string
		color string
		want  string
	}{
		{"text", "green", "\033[32mtext\033[0m"},
		{"text", "cyan", "\033[36mtext\033[0m"},
		{"text", "yellow", "\033[33mtext\033[0m"},
		{"text", "white", "\033[97mtext\033[0m"},
		{"text", "red", "\033[31mtext\033[0m"},
		{"text", "unknown", "text"},
		{"text", "", "text"},
	}

	for _, tt := range tests {
		got := colorize(tt.input, tt.color)
		if got != tt.want {
			t.Errorf("colorize(%q, %q) = %q, want %q", tt.input, tt.color, got, tt.want)
		}
	}
}
