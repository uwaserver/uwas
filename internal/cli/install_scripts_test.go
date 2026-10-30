package cli

// Regression tests for the shell installers (F49601-F49603). They run the real
// scripts against fakes (curl, systemctl, a fake release) inside a temp dir.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not available: %v", rel, err)
	}
	return p
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// F49601: update.sh used `sudo systemctl restart uwas` even as root. On an
// image without sudo the update aborted (set -e) after the binary was already
// replaced, leaving the old process running with no restart.
func TestUpdateScriptRestartsAsRootWithoutSudo(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	script := repoFile(t, "update.sh")
	for _, tc := range []struct {
		name     string
		withSudo bool
	}{
		{"control: sudo present", true},
		{"root without sudo", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			shim := filepath.Join(work, "bin")
			fix := filepath.Join(work, "fix")
			for _, d := range []string{shim, fix} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, tool := range []string{"awk", "grep", "sed", "head", "mktemp", "basename", "dirname", "sha256sum", "which", "chmod", "mv", "rm", "pgrep", "printf", "cat", "tr", "cp"} {
				if p, err := exec.LookPath(tool); err == nil {
					_ = os.Symlink(p, filepath.Join(shim, tool))
				} else if tool != "pgrep" {
					t.Skipf("missing %s", tool)
				}
			}
			newBin := "#!/bin/sh\n[ \"$1\" = version ] && echo \"uwas v9.9.9\"\nexit 0\n"
			writeExec(t, filepath.Join(fix, "uwas-linux-amd64"), newBin)
			sum := sha256.Sum256([]byte(newBin))
			if err := os.WriteFile(filepath.Join(fix, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  uwas-linux-amd64\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			writeExec(t, filepath.Join(shim, "uwas"), "#!/bin/sh\necho \"uwas v0.0.1\"\n")
			// update.sh builds the asset name from uname -m; pin it to amd64.
			writeExec(t, filepath.Join(shim, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo x86_64;; *) echo Linux;; esac\n")
			writeExec(t, filepath.Join(shim, "curl"), `#!/bin/sh
out=""; url=""
while [ $# -gt 0 ]; do case "$1" in -o) out=$2; shift 2;; -w) shift 2;; -*) shift;; *) url=$1; shift;; esac; done
case "$url" in
  *api.github.com*) printf '{ "tag_name": "v9.9.9" }\n'; exit 0;;
  *SHA256SUMS) cp "`+fix+`/SHA256SUMS" "$out"; printf 200; exit 0;;
  *uwas-linux-*) cp "`+fix+`/uwas-linux-amd64" "$out"; printf 200; exit 0;;
esac
exit 22
`)
			writeExec(t, filepath.Join(shim, "systemctl"), "#!/bin/sh\ncase \"$1\" in is-active) exit 0;; restart) echo \"restart $2\" >> \""+work+"/restart.log\"; exit 0;; esac\nexit 0\n")
			// Pretend to be root: a root login shell on a minimal image.
			writeExec(t, filepath.Join(shim, "id"), "#!/bin/sh\n[ \"$1\" = \"-u\" ] && { echo 0; exit 0; }\nexit 0\n")
			if tc.withSudo {
				writeExec(t, filepath.Join(shim, "sudo"), "#!/bin/sh\nexec \"$@\"\n")
			}

			cmd := exec.Command("/bin/sh", script)
			cmd.Env = []string{"PATH=" + shim}
			out, err := cmd.CombinedOutput()
			restarts, _ := os.ReadFile(filepath.Join(work, "restart.log"))
			t.Logf("EXPECTED: exit 0 and one restart\nACTUAL: err=%v restarts=%q\n%s", err, strings.TrimSpace(string(restarts)), out)
			if err != nil {
				t.Fatalf("PROBLEM CONFIRMED: update.sh failed: %v", err)
			}
			if got := strings.TrimSpace(string(restarts)); got != "restart uwas" {
				t.Fatalf("service not restarted after the update: %q", got)
			}
		})
	}
}

// F49602: scripts/install.sh left WatchdogSec=60 active whenever uwas.yaml
// merely contained a `watchdog:` key, even with `enabled: false`. UWAS pings
// systemd only when global.watchdog.enabled is true, so systemd restarted a
// healthy server every 60 seconds.
func TestInstallScriptWatchdogGate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("no awk")
	}
	src, err := os.ReadFile(repoFile(t, "scripts/install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "watchdog_configured() {")
	if start < 0 {
		t.Fatal("PROBLEM CONFIRMED: scripts/install.sh has no watchdog_configured check (a bare `watchdog:` key enables WatchdogSec)")
	}
	end := strings.Index(text[start:], "\n}\n")
	fn := text[start : start+end+3]

	cases := []struct {
		name, yaml string
		want       bool
	}{
		{"no watchdog key (control)", "global:\n  http_listen: \":80\"\n", false},
		{"enabled: true (control)", "global:\n  watchdog:\n    enabled: true\n", true},
		{"enabled: false", "global:\n  watchdog:\n    enabled: false\n", false},
		{"block without enabled", "global:\n  watchdog:\n    interval: 15s\n", false},
		{"empty flow mapping", "global:\n  watchdog: {}\n", false},
		{"self_restart only", "global:\n  watchdog:\n    self_restart: true\n    interval: 15s\n", false},
		{"enabled true with comment, then another block", "global:\n  watchdog:\n    enabled: true   # on\n  timeouts:\n    read: 30s\n", true},
		{"false before a later enabled: true in another block", "global:\n  watchdog:\n    enabled: false\n  admin:\n    enabled: true\n", false},
		{"inline mapping", "global:\n  watchdog: {enabled: true}\n", true},
		{"commented out", "global:\n  # watchdog:\n  #   enabled: true\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "uwas.yaml")
			if err := os.WriteFile(cfg, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", fn+"\nwatchdog_configured \"$1\"", "bash", cfg)
			err := cmd.Run()
			got := err == nil
			if got != tc.want {
				t.Fatalf("watchdog_configured = %v, want %v", got, tc.want)
			}
		})
	}
}

// F49603: the resilience drop-in written by scripts/install.sh must be removed
// by both uninstallers; otherwise it outlives the unit and re-applies
// WatchdogSec/Restart=always to whatever is installed under that name next.
func TestUninstallersRemoveTheResilienceDropIn(t *testing.T) {
	install, err := os.ReadFile(repoFile(t, "scripts/install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const dropIn = "/etc/systemd/system/uwas.service.d"
	if !strings.Contains(string(install), dropIn) {
		t.Skip("installer no longer writes a drop-in")
	}
	uninstall, err := os.ReadFile(repoFile(t, "scripts/uninstall.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(uninstall), dropIn) {
		t.Errorf("PROBLEM CONFIRMED: scripts/uninstall.sh leaves %s behind", dropIn)
	}
	goSrc, err := os.ReadFile("install.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goSrc), dropIn) {
		t.Errorf("PROBLEM CONFIRMED: `uwas uninstall` leaves %s behind", dropIn)
	}
}
