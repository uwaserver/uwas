package integration

// F52600 series: the CLI's own unit tests answer 200 to anything, so a command
// whose request the real admin API refuses (missing CSRF header, missing
// confirmation, wrong path, reshaped response) was never noticed (F51000 and
// F51001 were found this way). This drives every command that talks to the
// admin API against a real server, in both authentication modes.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cli"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/server"
)

// runCLIStdout runs a CLI command and returns what it printed and its error.
func runCLIStdout(t *testing.T, cmd cli.Command, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	runErr := cmd.Run(args)
	w.Close()
	os.Stdout = old
	out := <-done
	return out, runErr
}

type cliCase struct {
	name string
	cmd  cli.Command
	args []string
	// wantErr is a substring the error must contain; empty means success.
	wantErr string
	// wantOut is a substring the output must contain.
	wantOut string
}

func cliAPICases(root string) []cliCase {
	return []cliCase{
		{name: "status", cmd: &cli.StatusCommand{}, args: nil, wantOut: "Status:"},
		{name: "reload", cmd: &cli.ReloadCommand{}, args: nil, wantOut: "Config reloaded"},
		{name: "domain list", cmd: &cli.DomainCommand{}, args: []string{"list"}, wantOut: "cli-keep.test"},
		{name: "domain add", cmd: &cli.DomainCommand{}, args: []string{"add", "cli-new.test", "--type", "static", "--root", root, "--ssl", "off"}},
		{name: "domain remove", cmd: &cli.DomainCommand{}, args: []string{"remove", "cli-new.test"}},
		{name: "domain remove unknown", cmd: &cli.DomainCommand{}, args: []string{"remove", "nope.test"}, wantErr: "not found"},
		{name: "cache purge", cmd: &cli.CacheCommand{}, args: []string{"purge"}},
		{name: "cache purge tag", cmd: &cli.CacheCommand{}, args: []string{"purge", "--tag", "site:blog"}},
		{name: "cache stats", cmd: &cli.CacheCommand{}, args: []string{"stats"}},
		{name: "cert list", cmd: &cli.CertCommand{}, args: []string{"list"}},
		{name: "cert renew unknown", cmd: &cli.CertCommand{}, args: []string{"renew", "nope.test"}, wantErr: "not found"},
		{name: "php list", cmd: &cli.PHPCommand{}, args: []string{"list"}},
		{name: "php config unknown", cmd: &cli.PHPCommand{}, args: []string{"config", "9.9"}, wantErr: "API error"},
		{name: "php extensions unknown", cmd: &cli.PHPCommand{}, args: []string{"extensions", "9.9"}, wantErr: "API error"},
		{name: "php start unknown", cmd: &cli.PHPCommand{}, args: []string{"start", "9.9", "--port", "9099"}, wantErr: "API error"},
		{name: "php update unknown", cmd: &cli.PHPCommand{}, args: []string{"update", "9.9"}, wantErr: "API error"},
		{name: "php uninstall unknown", cmd: &cli.PHPCommand{}, args: []string{"uninstall", "9.9"}, wantErr: "not installed"},
		{name: "php stop unknown", cmd: &cli.PHPCommand{}, args: []string{"stop", "9.9"}, wantErr: "API error"},
	}
}

// cliServer starts a real server from a config FILE (the CLI's reload and the
// panel's persistence both need one) with the cache enabled, and returns the
// admin base URL, a document root, and the config path.
func cliServer(t *testing.T, keyed bool) (adminBase, root, cfgPath string) {
	t.Helper()
	return cliServerPin(t, keyed, "")
}

func cliServerPin(t *testing.T, keyed bool, pin string) (adminBase, root, cfgPath string) {
	t.Helper()
	webRoot := t.TempDir()
	root = filepath.Join(webRoot, "cli-keep.test", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "uwas.yaml")
	for attempt := 0; attempt < 3; attempt++ {
		httpAddr := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
		adminAddr := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
		keyLine := ""
		if keyed {
			keyLine = `    api_key: "test-api-key"` + "\n"
		}
		if pin != "" {
			keyLine += fmt.Sprintf(`    pin_code: "%s"`+"\n", pin)
		}
		yaml := fmt.Sprintf(`global:
  worker_count: "1"
  http_listen: "%s"
  web_root: "%s"
  pid_file: "%s"
  log_level: error
  log_format: text
  admin:
    enabled: true
    listen: "%s"
%s  cache:
    enabled: true
    memory_limit: 16MB
    default_ttl: 60
  timeouts:
    read: 5s
    write: 5s
    idle: 5s
    shutdown_grace: 2s

domains:
  - host: "cli-keep.test"
    root: "%s"
    type: static
    ssl:
      mode: "off"
`, httpAddr, strings.ReplaceAll(webRoot, "\\", "/"), strings.ReplaceAll(filepath.Join(dir, "uwas.pid"), "\\", "/"),
			adminAddr, keyLine, strings.ReplaceAll(root, "\\", "/"))
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		srv := server.New(cfg, logger.New("error", "text"))
		srv.SetConfigPath(cfgPath)
		go srv.Start()
		adminBase = "http://" + adminAddr
		if waitForAdminReady(adminBase, 3*time.Second) {
			return adminBase, root, cfgPath
		}
	}
	t.Fatal("admin server did not become ready")
	return
}

func runCLICases(t *testing.T, adminBase, root string, keyed bool) {
	t.Helper()
	for _, c := range cliAPICases(root) {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{}, c.args...)
			args = append(args, "--api-url", adminBase)
			if keyed {
				args = append(args, "--api-key", "test-api-key")
			}
			out, err := runCLIStdout(t, c.cmd, args...)
			t.Logf("ARGS: %v\nOUT: %s\nERR: %v", c.args, strings.TrimSpace(out), err)
			if c.wantErr == "" && err != nil {
				t.Fatalf("PROBLEM CONFIRMED: %s failed against the real admin API: %v", c.name, err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("PROBLEM CONFIRMED: %s: err = %v, want it to contain %q", c.name, err, c.wantErr)
			}
			if c.wantOut != "" && !strings.Contains(out, c.wantOut) {
				t.Fatalf("PROBLEM CONFIRMED: %s output %q lacks %q", c.name, out, c.wantOut)
			}
		})
	}
}

func TestCLICommandsKeyedAdminAPI(t *testing.T) {
	adminBase, root, _ := cliServer(t, true)
	runCLICases(t, adminBase, root, true)
}

func TestCLICommandsKeylessLoopbackAdminAPI(t *testing.T) {
	adminBase, root, _ := cliServer(t, false)
	runCLICases(t, adminBase, root, false)
}

// F52600: a server with pin_code set (every `uwas init` / `uwas install` writes
// one) gates DELETE /domains/{host} behind X-Pin-Code. The CLI had no way to
// send it, so `uwas domain remove` answered 403 pin_required forever.
func TestCLIDomainRemoveOnPinProtectedServer(t *testing.T) {
	const pin = "482913"
	adminBase, root, _ := cliServerPin(t, true, pin)
	cmd := &cli.DomainCommand{}
	base := []string{"--api-url", adminBase, "--api-key", "test-api-key"}
	add := func(host string) {
		t.Helper()
		if _, err := runCLIStdout(t, cmd, append([]string{"add", host, "--type", "static", "--root", root, "--ssl", "off"}, base...)...); err != nil {
			t.Fatalf("control: domain add must not need the PIN: %v", err)
		}
	}
	remove := func(args ...string) error {
		_, err := runCLIStdout(t, cmd, append(append([]string{"remove"}, args...), base...)...)
		return err
	}

	add("pin-flag.test")
	err := remove("pin-flag.test")
	t.Logf("EXPECTED: a hint about the PIN\nACTUAL: %v", err)
	if err == nil || !strings.Contains(err.Error(), "UWAS_ADMIN_PIN") {
		t.Fatalf("PROBLEM CONFIRMED: removing without the PIN gave %v, want an error that says how to pass the PIN", err)
	}
	if err := remove("pin-flag.test", "--pin", "000000"); err == nil {
		t.Fatal("control: a wrong PIN must be refused")
	}
	if err := remove("pin-flag.test", "--pin", pin); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: domain remove --pin <PIN> failed: %v", err)
	}
	if hasHost(adminDomainHosts(t, adminBase), "pin-flag.test") {
		t.Fatal("PROBLEM CONFIRMED: domain still configured after a removal with the PIN")
	}

	add("pin-env.test")
	t.Setenv("UWAS_ADMIN_PIN", pin)
	if err := remove("pin-env.test"); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: domain remove with UWAS_ADMIN_PIN set failed: %v", err)
	}
	if hasHost(adminDomainHosts(t, adminBase), "pin-env.test") {
		t.Fatal("PROBLEM CONFIRMED: domain still configured after a removal with UWAS_ADMIN_PIN")
	}
	// The PIN reaches only the commands that need it: a wrong environment PIN
	// must not break commands that do not check it.
	t.Setenv("UWAS_ADMIN_PIN", "000000")
	if _, err := runCLIStdout(t, &cli.StatusCommand{}, base...); err != nil {
		t.Fatalf("control: status must not depend on the PIN: %v", err)
	}
}

// How the CLI finds its credentials and the server: --api-key beats
// UWAS_ADMIN_KEY, a missing or wrong key is refused with a message that says
// where the key comes from, and --api-url falls back to the config file's
// admin address.
func TestCLICredentialSources(t *testing.T) {
	adminBase, _, cfgPath := cliServer(t, true)
	status := &cli.StatusCommand{}
	list := &cli.DomainCommand{}

	t.Setenv("UWAS_ADMIN_KEY", "test-api-key")
	if _, err := runCLIStdout(t, list, "list", "--api-url", adminBase); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: UWAS_ADMIN_KEY was not used: %v", err)
	}
	if _, err := runCLIStdout(t, list, "list", "--api-url", adminBase, "--api-key", "wrong"); err == nil {
		t.Fatal("PROBLEM CONFIRMED: --api-key must override UWAS_ADMIN_KEY (a wrong flag was accepted)")
	}

	t.Setenv("UWAS_ADMIN_KEY", "")
	_, err := runCLIStdout(t, list, "list", "--api-url", adminBase)
	t.Logf("EXPECTED: 401 with a hint about UWAS_ADMIN_KEY\nACTUAL: %v", err)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "UWAS_ADMIN_KEY") {
		t.Fatalf("PROBLEM CONFIRMED: a missing key gave %v, want 401 and a hint naming UWAS_ADMIN_KEY", err)
	}

	// F52601: /health is public, so `status` with a missing or wrong key used to
	// print "Status: ok / Domains: 0" and exit 0 (the stats error was dropped
	// and the empty body reported as a malformed response).
	out, err := runCLIStdout(t, status, "--api-url", adminBase, "--api-key", "wrong")
	t.Logf("EXPECTED: an error that names UWAS_ADMIN_KEY\nACTUAL: err=%v out=%q", err, out)
	if err == nil || !strings.Contains(err.Error(), "UWAS_ADMIN_KEY") {
		t.Fatalf("PROBLEM CONFIRMED: status with a wrong key reported %v (out %q), want a credentials error", err, out)
	}

	// --api-url from the config file found in the working directory.
	t.Chdir(filepath.Dir(cfgPath))
	t.Setenv("UWAS_ADMIN_KEY", "test-api-key")
	if _, err := runCLIStdout(t, status); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: status without --api-url did not find the server through ./uwas.yaml: %v", err)
	}
}

// restart and stop act on the PID file: a missing file must be an error (exit
// code 1 from CLI.Run), not a silent success.
func TestCLIStopRestartWithoutPIDFile(t *testing.T) {
	adminBase, _, _ := cliServer(t, true)
	missing := filepath.Join(t.TempDir(), "no.pid")
	if _, err := runCLIStdout(t, &cli.StopCommand{}, "--pid-file", missing); err == nil {
		t.Fatal("PROBLEM CONFIRMED: stop with a missing PID file reported success")
	}
	if _, err := runCLIStdout(t, &cli.RestartCommand{}, "--pid-file", missing, "--api-url", adminBase, "--api-key", "test-api-key"); err == nil {
		t.Fatal("PROBLEM CONFIRMED: restart with a missing PID file reported success")
	}
}

// What the panel/API persists must be what the offline commands read: a domain
// added through the running server has to show up in `config validate/test`,
// survive `backup` and come back with `restore`.
func TestCLIConfigBackupRestoreRoundTrip(t *testing.T) {
	adminBase, root, cfgPath := cliServer(t, true)
	base := []string{"--api-url", adminBase, "--api-key", "test-api-key"}
	if _, err := runCLIStdout(t, &cli.DomainCommand{}, append([]string{"add", "persisted.test", "--type", "static", "--root", root, "--ssl", "off"}, base...)...); err != nil {
		t.Fatalf("domain add: %v", err)
	}

	out, err := runCLIStdout(t, &cli.ConfigCommand{}, "test", "-c", cfgPath)
	t.Logf("config test: %v\n%s", err, out)
	if err != nil || !strings.Contains(out, "persisted.test") {
		t.Fatalf("PROBLEM CONFIRMED: `config test` does not see the domain the server persisted (err %v):\n%s", err, out)
	}
	if out, err := runCLIStdout(t, &cli.ConfigCommand{}, "validate", "-c", cfgPath); err != nil || !strings.Contains(out, "2 domains") {
		t.Fatalf("PROBLEM CONFIRMED: `config validate` failed on the persisted config: %v\n%s", err, out)
	}

	dir := t.TempDir()
	archive := filepath.Join(dir, "backup.tar.gz")
	certs := filepath.Join(dir, "certs")
	if err := os.MkdirAll(certs, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLIStdout(t, &cli.BackupCommand{}, "--output", archive, "-c", cfgPath, "--certs", certs); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: backup failed: %v\n%s", err, out)
	}
	restoredCfg := filepath.Join(dir, "restored")
	restoredCerts := filepath.Join(dir, "restored-certs")
	if out, err := runCLIStdout(t, &cli.RestoreCommand{}, "--input", archive, "--config-dir", restoredCfg, "--certs-dir", restoredCerts); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: restore failed: %v\n%s", err, out)
	}
	cfg, err := config.Load(filepath.Join(restoredCfg, "uwas.yaml"))
	if err != nil {
		t.Fatalf("PROBLEM CONFIRMED: the restored configuration does not load: %v", err)
	}
	found := false
	for _, d := range cfg.Domains {
		if d.Host == "persisted.test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("PROBLEM CONFIRMED: persisted.test is missing after backup and restore: %+v", cfg.Domains)
	}
}

// F52603: `uwas backup` without -c looked for ./uwas.yaml only, while serve,
// config and every other command resolve the config like findConfig does
// (./.uwas, ~/.uwas, /etc/uwas). On an installed server the backup was written
// WITHOUT the configuration (a warning on stderr, exit code 0).
func TestCLIBackupFindsTheServerConfig(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".uwas", "domains.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "global:\n  http_listen: \"127.0.0.1:18080\"\ndomains: []\n"
	if err := os.WriteFile(filepath.Join(work, ".uwas", "uwas.yaml"), []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".uwas", "domains.d", "extra.test.yaml"), []byte("host: extra.test\nroot: /tmp\ntype: static\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	if out, err := runCLIStdout(t, &cli.ConfigCommand{}, "validate"); err != nil {
		t.Fatalf("control: config validate must find ./.uwas/uwas.yaml: %v\n%s", err, out)
	}
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	certs := t.TempDir()
	out, err := runCLIStdout(t, &cli.BackupCommand{}, "--output", archive, "--certs", certs)
	t.Logf("backup: err=%v out=%s", err, out)
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	names := tarNames(t, archive)
	t.Logf("EXPECTED: config/uwas.yaml and config/domains.d/extra.test.yaml in the archive\nACTUAL: %v", names)
	has := func(want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}
	if !has("config/uwas.yaml") || !has("config/domains.d/extra.test.yaml") {
		t.Fatalf("PROBLEM CONFIRMED: the backup did not include the server's configuration: %v", names)
	}
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}

// F52604: serve's help documents `-c, --config`, but only -c was defined, so
// `uwas serve --config <file>` died with "flag provided but not defined"; and
// an explicit config that does not exist was reported with the Windows wording
// "The system cannot find the file specified".
func TestCLIServeConfigFlagAndMissingFileMessage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	for _, flagName := range []string{"-c", "--config"} {
		_, err := runCLIStdout(t, &cli.ServeCommand{}, flagName, missing)
		t.Logf("serve %s %s: %v", flagName, missing, err)
		if err == nil || strings.Contains(err.Error(), "not defined") {
			t.Fatalf("PROBLEM CONFIRMED: serve %s must be accepted and report the missing file, got %v", flagName, err)
		}
		if strings.Contains(err.Error(), "The system cannot find") {
			t.Fatalf("PROBLEM CONFIRMED: serve %s reported a Windows-only message on %s: %v", flagName, runtime.GOOS, err)
		}
		if !strings.Contains(err.Error(), missing) {
			t.Fatalf("PROBLEM CONFIRMED: the error must name the file %s: %v", missing, err)
		}
	}
}
