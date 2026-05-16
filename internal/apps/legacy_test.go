package apps

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestMapLegacyRuntime(t *testing.T) {
	cases := map[string]Runtime{
		"node":    RuntimeNode,
		"nodejs":  RuntimeNode,
		"Node.js": RuntimeNode,
		"python":  RuntimePython,
		"PY":      RuntimePython,
		"ruby":    RuntimeRuby,
		"go":      RuntimeGo,
		"golang":  RuntimeGo,
		"docker":  RuntimeDocker,
		"":        RuntimeCustom,
		"unknown": RuntimeCustom,
	}
	for in, want := range cases {
		if got := mapLegacyRuntime(in); got != want {
			t.Errorf("mapLegacyRuntime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConvertDomainToProxy(t *testing.T) {
	src := config.Domain{
		Host: "api.example.com",
		Type: "app",
		Root: "/var/www/api",
		App: config.AppConfig{
			Runtime: "node",
			Command: "node index.js",
			Port:    3001,
		},
	}
	got := convertDomainToProxy(src, "api-example-com")
	if got.Type != "proxy" {
		t.Fatalf("type = %q, want proxy", got.Type)
	}
	if got.App.Runtime != "" || got.App.Command != "" || got.App.Port != 0 {
		t.Fatalf("legacy App block not cleared: %+v", got.App)
	}
	if len(got.Proxy.Upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(got.Proxy.Upstreams))
	}
	if got.Proxy.Upstreams[0].Address != "apps://api-example-com" {
		t.Errorf("upstream address = %q, want apps://api-example-com",
			got.Proxy.Upstreams[0].Address)
	}
	if got.Host != "api.example.com" || got.Root != "/var/www/api" {
		t.Errorf("non-app fields mutated: host=%q root=%q", got.Host, got.Root)
	}
}

func TestCloneStringMap(t *testing.T) {
	if got := cloneStringMap(nil); got != nil {
		t.Errorf("nil input should yield nil, got %v", got)
	}
	src := map[string]string{"FOO": "bar", "BAZ": "qux"}
	dst := cloneStringMap(src)
	if len(dst) != 2 || dst["FOO"] != "bar" || dst["BAZ"] != "qux" {
		t.Fatalf("clone mismatch: %v", dst)
	}
	dst["FOO"] = "changed"
	if src["FOO"] != "bar" {
		t.Errorf("clone is not independent: src mutated to %q", src["FOO"])
	}
}

func TestLooselyEqual(t *testing.T) {
	base := &App{
		Runtime: RuntimeNode,
		Command: "node x.js",
		Port:    3001,
		WorkDir: "/var/www/x",
		Env:     map[string]string{"NODE_ENV": "prod"},
	}
	same := &App{
		Runtime: RuntimeNode,
		Command: "node x.js",
		Port:    3001,
		WorkDir: "/var/www/x",
		Env:     map[string]string{"NODE_ENV": "prod"},
	}
	if !looselyEqual(base, same) {
		t.Error("identical configs should be equal")
	}

	differentCmd := *same
	differentCmd.Command = "node y.js"
	if looselyEqual(base, &differentCmd) {
		t.Error("different command should not be equal")
	}

	differentEnv := *same
	differentEnv.Env = map[string]string{"NODE_ENV": "dev"}
	if looselyEqual(base, &differentEnv) {
		t.Error("different env should not be equal")
	}

	extraEnv := *same
	extraEnv.Env = map[string]string{"NODE_ENV": "prod", "EXTRA": "y"}
	if looselyEqual(base, &extraEnv) {
		t.Error("extra env key should not be equal")
	}
}

func TestMigrateLegacyDomainsDryRun(t *testing.T) {
	// nil manager → dry-run: report should describe what WOULD happen.
	domains := []config.Domain{
		{Host: "api.example.com", Type: "app", Root: "/var/www/api",
			App: config.AppConfig{Runtime: "node", Command: "node x.js", Port: 3001}},
		{Host: "blog.example.com", Type: "php", Root: "/var/www/blog"},
		{Host: "worker.example.com", Type: "app", Root: "/var/www/worker",
			App: config.AppConfig{Runtime: "python", Command: "python w.py"}},
	}
	report, updated, autoStart := MigrateLegacyDomains(nil, domains)

	if len(report.Migrated) != 2 {
		t.Errorf("expected 2 migrated entries, got %d", len(report.Migrated))
	}
	if len(report.Skipped) != 0 || len(report.Conflicts) != 0 {
		t.Errorf("expected no skipped/conflicts, got skipped=%d conflicts=%d",
			len(report.Skipped), len(report.Conflicts))
	}
	if len(updated) != 3 {
		t.Fatalf("expected 3 updated domains, got %d", len(updated))
	}
	// The php domain should be untouched.
	if updated[1].Type != "php" {
		t.Errorf("non-app domain was mutated: %+v", updated[1])
	}
	// app domains should now be proxy with apps:// upstream.
	for _, i := range []int{0, 2} {
		if updated[i].Type != "proxy" {
			t.Errorf("domain %d type = %q, want proxy", i, updated[i].Type)
		}
	}
	// autoStart should be empty in dry-run.
	if len(autoStart) != 0 {
		t.Errorf("dry-run should return no autoStart names, got %v", autoStart)
	}
}
