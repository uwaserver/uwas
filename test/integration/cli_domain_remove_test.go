package integration

// F51000: `uwas domain remove <host>` sent DELETE /api/v1/domains/<host> with no
// ?confirm=true, which the admin API requires (400 "missing confirmation"). The
// CLI's own unit tests answered 200 to anything, so the command never worked
// against a real server: the operator got an API error and the domain stayed.
// This drives the real CLI command against the real admin API.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cli"
	"github.com/uwaserver/uwas/internal/config"
)

func adminDomainHosts(t *testing.T, adminBase string) []string {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/domains?limit=500", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out struct {
		Items []struct {
			Host string `json:"host"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("domains list: %v (%s)", err, data)
	}
	hosts := make([]string, 0, len(out.Items))
	for _, it := range out.Items {
		hosts = append(hosts, it.Host)
	}
	return hosts
}

func hasHost(hosts []string, want string) bool {
	for _, h := range hosts {
		if h == want {
			return true
		}
	}
	return false
}

func TestCLIDomainRemoveAgainstRealAdminAPI(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	webRoot := t.TempDir()
	root := filepath.Join(webRoot, "gone.test", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Global.WebRoot = webRoot
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: root, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		{Host: "gone.test", Root: root, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		{Host: "keep.test", Root: root, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	if !hasHost(adminDomainHosts(t, adminBase), "gone.test") {
		t.Fatal("control: gone.test missing before removal")
	}

	cmd := &cli.DomainCommand{}
	err := cmd.Run([]string{"remove", "--api-url", adminBase, "--api-key", "test-api-key", "gone.test"})
	t.Logf("EXPECTED: nil error and gone.test removed\nACTUAL: err=%v", err)
	if err != nil {
		t.Fatalf("PROBLEM CONFIRMED: uwas domain remove failed against the real admin API: %v", err)
	}
	hosts := adminDomainHosts(t, adminBase)
	if hasHost(hosts, "gone.test") {
		t.Fatalf("PROBLEM CONFIRMED: gone.test still configured after removal: %v", hosts)
	}
	if !hasHost(hosts, "keep.test") {
		t.Fatalf("control: keep.test must stay configured: %v", hosts)
	}
	if err := cmd.Run([]string{"remove", "--api-url", adminBase, "--api-key", "test-api-key", "never-existed.test"}); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("control: removing an unknown host must report not found, got %v", err)
	}
}

// F51001: the CLI never sent X-Requested-With, so every state-changing command
// (domain add/remove, php, cache purge, cert renew, backup...) was refused by a
// server with an API key ("csrf: invalid origin"). Only keyless loopback mode,
// where a missing Origin is accepted, ever worked.
func TestCLIDomainAddAgainstRealAdminAPI(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	webRoot := t.TempDir()
	root := filepath.Join(webRoot, "fresh.test", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Global.WebRoot = webRoot
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: root, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	cmd := &cli.DomainCommand{}
	err := cmd.Run([]string{"add", "fresh.test", "--type", "static", "--root", root, "--ssl", "off",
		"--api-url", adminBase, "--api-key", "test-api-key"})
	t.Logf("EXPECTED: nil error and fresh.test configured\nACTUAL: err=%v", err)
	if err != nil {
		t.Fatalf("PROBLEM CONFIRMED: uwas domain add failed against the real admin API: %v", err)
	}
	if !hasHost(adminDomainHosts(t, adminBase), "fresh.test") {
		t.Fatalf("PROBLEM CONFIRMED: fresh.test not configured after add")
	}
	if err := cmd.Run([]string{"remove", "fresh.test", "--api-url", adminBase, "--api-key", "wrong-key"}); err == nil {
		t.Fatal("control: a wrong API key must still be refused")
	}
	if !hasHost(adminDomainHosts(t, adminBase), "fresh.test") {
		t.Fatal("control: a refused removal must leave the domain configured")
	}
}
