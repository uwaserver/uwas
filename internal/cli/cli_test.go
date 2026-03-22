package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCLIHelpOutput(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})
	app.Register(&ServeCommand{})
	app.Register(&ConfigCommand{})
	app.Register(NewHelpCommand(app))

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	app.Run([]string{"help"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "serve") {
		t.Error("help should list serve command")
	}
	if !strings.Contains(output, "version") {
		t.Error("help should list version command")
	}
	if !strings.Contains(output, "config") {
		t.Error("help should list config command")
	}
}

func TestVersionCommand(t *testing.T) {
	cmd := &VersionCommand{}
	if cmd.Name() != "version" {
		t.Errorf("name = %q", cmd.Name())
	}

	// Should not error
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Run error: %v", err)
	}
	if !strings.Contains(buf.String(), "uwas") {
		t.Error("version output should contain 'uwas'")
	}
}

func TestConfigValidateCommand(t *testing.T) {
	// Create a temp valid config
	tmpFile, err := os.CreateTemp("", "uwas-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString(`
domains:
  - host: test.com
    root: /tmp
    type: static
    ssl:
      mode: off
`)
	tmpFile.Close()

	cmd := &ConfigCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err = cmd.Run([]string{"validate", "-c", tmpFile.Name()})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Errorf("validate should succeed: %v", err)
	}
}

func TestConfigValidateInvalid(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "uwas-invalid-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString(`not valid yaml {{`)
	tmpFile.Close()

	cmd := &ConfigCommand{}
	err = cmd.Run([]string{"validate", "-c", tmpFile.Name()})
	if err == nil {
		t.Error("should fail for invalid config")
	}
}

func TestCLIRegisterAndLookup(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})

	if len(app.commands) != 1 {
		t.Errorf("commands = %d, want 1", len(app.commands))
	}
}

func TestServeCommandHelp(t *testing.T) {
	cmd := &ServeCommand{}
	help := cmd.Help()
	if help == "" {
		t.Error("Help() should not be empty")
	}
	if !strings.Contains(help, "--config") {
		t.Error("Help should mention --config flag")
	}
	if !strings.Contains(help, "--log-level") {
		t.Error("Help should mention --log-level flag")
	}
}

func TestServeCommandNameDescription(t *testing.T) {
	cmd := &ServeCommand{}
	if cmd.Name() != "serve" {
		t.Errorf("Name() = %q, want serve", cmd.Name())
	}
	if cmd.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestHelpCommandRunWithSpecificSubcommand(t *testing.T) {
	app := New()
	app.Register(&ServeCommand{})
	app.Register(&VersionCommand{})
	app.Register(&ConfigCommand{})
	helpCmd := NewHelpCommand(app)
	app.Register(helpCmd)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := helpCmd.Run([]string{"serve"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(output, "serve") {
		t.Error("help for serve should mention 'serve'")
	}
	// Since ServeCommand implements Help(), the detailed help should be printed
	if !strings.Contains(output, "--config") {
		t.Error("help for serve should include detailed help with --config")
	}
}

func TestHelpCommandRunWithUnknownCommand(t *testing.T) {
	app := New()
	helpCmd := NewHelpCommand(app)

	err := helpCmd.Run([]string{"nonexistent"})
	if err == nil {
		t.Error("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, should mention unknown command", err.Error())
	}
}

func TestHelpCommandRunNoArgs(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})
	helpCmd := NewHelpCommand(app)
	app.Register(helpCmd)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := helpCmd.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(buf.String(), "UWAS") {
		t.Error("should print usage info")
	}
}

func TestHelpCommandNameDescription(t *testing.T) {
	app := New()
	helpCmd := NewHelpCommand(app)

	if helpCmd.Name() != "help" {
		t.Errorf("Name() = %q, want help", helpCmd.Name())
	}
	if helpCmd.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestHelpCommandRunWithCommandWithoutHelper(t *testing.T) {
	app := New()
	// VersionCommand does NOT implement Help() interface
	app.Register(&VersionCommand{})
	helpCmd := NewHelpCommand(app)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := helpCmd.Run([]string{"version"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// Should still print the basic info
	if !strings.Contains(buf.String(), "version") {
		t.Error("should print version command info")
	}
}

func TestServeCommandRunNonexistentConfig(t *testing.T) {
	cmd := &ServeCommand{}
	err := cmd.Run([]string{"-c", "/nonexistent/path/uwas-does-not-exist.yaml"})
	if err == nil {
		t.Fatal("expected error for nonexistent config file")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %q, should mention loading config", err.Error())
	}
}

func TestCLIRunEmptyArgs(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})
	app.Register(&ServeCommand{})
	app.Register(NewHelpCommand(app))

	// CLI.Run with empty args calls printUsage() then os.Exit(0).
	// We cannot intercept os.Exit directly, but we can test printUsage
	// by calling the help command with no args which exercises the same code path.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	helpCmd := NewHelpCommand(app)
	err := helpCmd.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(output, "UWAS") {
		t.Error("usage output should contain 'UWAS'")
	}
	if !strings.Contains(output, "uwas <command>") {
		t.Error("usage output should contain 'uwas <command>'")
	}
	if !strings.Contains(output, "version") {
		t.Error("usage output should list 'version' command")
	}
	if !strings.Contains(output, "serve") {
		t.Error("usage output should list 'serve' command")
	}
}

// ========== domain.go tests ==========

func TestDomainCommandNameDescription(t *testing.T) {
	d := &DomainCommand{}
	if d.Name() != "domain" {
		t.Errorf("Name() = %q, want domain", d.Name())
	}
	if d.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestDomainCommandHelp(t *testing.T) {
	d := &DomainCommand{}
	h := d.Help()
	if !strings.Contains(h, "list") {
		t.Error("Help should mention list")
	}
	if !strings.Contains(h, "add") {
		t.Error("Help should mention add")
	}
	if !strings.Contains(h, "remove") {
		t.Error("Help should mention remove")
	}
}

func TestDomainCommandRunNoArgs(t *testing.T) {
	d := &DomainCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := d.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Run(nil) error: %v", err)
	}
	// Should print help
	if !strings.Contains(buf.String(), "list") {
		t.Error("should print help with subcommands")
	}
}

func TestDomainCommandUnknownSubcommand(t *testing.T) {
	d := &DomainCommand{}
	err := d.Run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestDomainListCommand(t *testing.T) {
	// Start a mock server returning domain JSON.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/domains" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer testkey" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		domains := []map[string]any{
			{"host": "example.com", "type": "static", "ssl": "auto", "root": "/var/www"},
			{"host": "api.test.com", "type": "proxy", "ssl": "off", "root": ""},
		}
		json.NewEncoder(w).Encode(domains)
	}))
	defer srv.Close()

	d := &DomainCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := d.Run([]string{"list", "--api-url", srv.URL, "--api-key", "testkey"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if !strings.Contains(output, "example.com") {
		t.Errorf("output should contain example.com, got:\n%s", output)
	}
	if !strings.Contains(output, "api.test.com") {
		t.Errorf("output should contain api.test.com, got:\n%s", output)
	}
	if !strings.Contains(output, "HOST") {
		t.Errorf("output should contain header, got:\n%s", output)
	}
}

func TestDomainAddCommand(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.WriteHeader(201)
		fmt.Fprint(w, `{"Host":"newsite.com","Type":"static"}`)
	}))
	defer srv.Close()

	d := &DomainCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := d.Run([]string{"add", "--api-url", srv.URL, "--api-key", "key1",
		"--type", "static", "--root", "/var/www/new", "--ssl", "auto", "newsite.com"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("add error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/domains" {
		t.Errorf("path = %s", gotPath)
	}
	if !strings.Contains(gotBody, "newsite.com") {
		t.Errorf("body should contain host, got: %s", gotBody)
	}
	if !strings.Contains(buf.String(), "Domain added") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestDomainAddMissingHost(t *testing.T) {
	d := &DomainCommand{}
	err := d.Run([]string{"add", "--api-url", "http://localhost:0", "--type", "static"})
	if err == nil {
		t.Fatal("expected error for missing host")
	}
	if !strings.Contains(err.Error(), "host is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestDomainRemoveCommand(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		fmt.Fprint(w, `{"status":"deleted"}`)
	}))
	defer srv.Close()

	d := &DomainCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := d.Run([]string{"remove", "--api-url", srv.URL, "--api-key", "key1", "old.com"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("remove error: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/v1/domains/old.com" {
		t.Errorf("path = %s", gotPath)
	}
	if !strings.Contains(buf.String(), "Domain removed") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestDomainRemoveMissingHost(t *testing.T) {
	d := &DomainCommand{}
	err := d.Run([]string{"remove", "--api-url", "http://localhost:0"})
	if err == nil {
		t.Fatal("expected error for missing host")
	}
	if !strings.Contains(err.Error(), "host is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestDomainRemoveAliases(t *testing.T) {
	// Test "rm" and "delete" aliases
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"deleted"}`)
	}))
	defer srv.Close()

	d := &DomainCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := d.Run([]string{"rm", "--api-url", srv.URL, "--api-key", "k", "test.com"})
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("rm alias error: %v", err)
	}

	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w

	err = d.Run([]string{"delete", "--api-url", srv.URL, "--api-key", "k", "test2.com"})
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("delete alias error: %v", err)
	}
}

// ========== cache command tests ==========

func TestCacheCommandNameDescription(t *testing.T) {
	c := &CacheCommand{}
	if c.Name() != "cache" {
		t.Errorf("Name() = %q, want cache", c.Name())
	}
	if c.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestCacheCommandHelp(t *testing.T) {
	c := &CacheCommand{}
	h := c.Help()
	if !strings.Contains(h, "purge") {
		t.Error("Help should mention purge")
	}
	if !strings.Contains(h, "stats") {
		t.Error("Help should mention stats")
	}
}

func TestCacheCommandRunNoArgs(t *testing.T) {
	c := &CacheCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Run(nil) error: %v", err)
	}
	if !strings.Contains(buf.String(), "purge") {
		t.Error("should print help")
	}
}

func TestCacheCommandUnknownSubcommand(t *testing.T) {
	c := &CacheCommand{}
	err := c.Run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestCachePurgeCommand(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cache/purge" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		gotBody = buf.String()
		fmt.Fprint(w, `{"status":"all purged"}`)
	}))
	defer srv.Close()

	c := &CacheCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"purge", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("purge error: %v", err)
	}
	if !strings.Contains(buf.String(), "all purged") {
		t.Errorf("output = %q", buf.String())
	}
	// Without --tag, body should be "{}"
	if strings.Contains(gotBody, "tag") {
		t.Errorf("body should not contain tag field, got: %s", gotBody)
	}
}

func TestCachePurgeWithTag(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		gotBody = buf.String()
		fmt.Fprint(w, `{"status":"purged","tag":"blog"}`)
	}))
	defer srv.Close()

	c := &CacheCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"purge", "--api-url", srv.URL, "--api-key", "k", "--tag", "blog"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("purge with tag error: %v", err)
	}
	if !strings.Contains(gotBody, "blog") {
		t.Errorf("body should contain tag, got: %s", gotBody)
	}
}

func TestCacheStatsCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stats" {
			t.Errorf("path = %s, want /api/v1/stats", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"requests_total": 100,
			"cache_hits":     50,
			"cache_misses":   50,
		})
	}))
	defer srv.Close()

	c := &CacheCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"stats", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("stats error: %v", err)
	}
	if !strings.Contains(output, "requests_total") {
		t.Errorf("output should contain requests_total, got:\n%s", output)
	}
}

func TestApiRequestError(t *testing.T) {
	// Server that returns 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":"internal error"}`)
	}))
	defer srv.Close()

	_, err := apiRequest("GET", srv.URL+"/api/v1/stats", "key", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "API error 500") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestApiRequestConnectionError(t *testing.T) {
	// Use a URL that will definitely fail to connect
	_, err := apiRequest("GET", "http://127.0.0.1:1/nonexistent", "", nil)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
	if !strings.Contains(err.Error(), "API request failed") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestApiRequestWithAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	_, err := apiRequest("GET", srv.URL+"/test", "mykey", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotAuth != "Bearer mykey" {
		t.Errorf("auth = %q, want 'Bearer mykey'", gotAuth)
	}
}

func TestApiRequestWithoutAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	_, err := apiRequest("GET", srv.URL+"/test", "", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("auth should be empty, got %q", gotAuth)
	}
}

func TestDomainListBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	d := &DomainCommand{}
	err := d.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %q", err.Error())
	}
}
