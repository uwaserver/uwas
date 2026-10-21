package phpmanager

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func withApt(id, codename string, candidate func(string) string, fn func()) {
	origGOOS, origRead, origCand := runtimeGOOSInstall, readOSRelease, aptCandidate
	defer func() { runtimeGOOSInstall, readOSRelease, aptCandidate = origGOOS, origRead, origCand }()
	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte(fmt.Sprintf("ID=%s\nVERSION_ID=1\nVERSION_CODENAME=%s\nPRETTY_NAME=\"Test OS\"\n", id, codename)), nil
	}
	aptCandidate = candidate
	fn()
}

func TestInstallInfoUsesDistroArchiveWhenAvailable(t *testing.T) {
	withApt("ubuntu", "noble", func(string) string { return "8.3.6-0ubuntu0.24.04.9" }, func() {
		info := GetInstallInfo("8.3")
		all := strings.Join(info.Commands, "\n")
		if strings.Contains(all, "sury") || strings.Contains(all, "ondrej") || strings.Contains(all, "imagick") {
			t.Fatalf("distro install must not add third-party repos or imagick:\n%s", all)
		}
		if !strings.Contains(all, "php8.3-fpm") || !strings.Contains(all, "apt-get install -y") {
			t.Fatalf("missing install command:\n%s", all)
		}
	})
}

func TestInstallInfoAddsSuryWhenDistroLacksVersion(t *testing.T) {
	withApt("ubuntu", "noble", func(string) string { return "" }, func() {
		info := GetInstallInfo("8.4")
		all := strings.Join(info.Commands, "\n")
		for _, want := range []string{
			"debsuryorg-archive-keyring.deb",
			"signed-by=/usr/share/keyrings/debsuryorg-archive-keyring.gpg] https://packages.sury.org/php/ noble main",
			"php8.4-fpm", "php8.4-imagick",
		} {
			if !strings.Contains(all, want) {
				t.Errorf("commands missing %q:\n%s", want, all)
			}
		}
		if strings.Contains(all, "ondrej") || strings.Contains(all, "add-apt-repository") {
			t.Errorf("the ondrej PPA must not be used:\n%s", all)
		}
	})
}

func TestInstallInfoUnknownCodenameFailsClosed(t *testing.T) {
	withApt("debian", "../evil", func(string) string { return "" }, func() {
		info := GetInstallInfo("8.4")
		for _, c := range info.Commands {
			if !strings.HasPrefix(c, "#") {
				t.Fatalf("unsupported release must produce comments only, got %q", c)
			}
		}
	})
}

func TestRunInstallShellStepUsesSh(t *testing.T) {
	origGOOS, origRead, origCand, origExec := runtimeGOOSInstall, readOSRelease, aptCandidate, installExecCommand
	defer func() {
		runtimeGOOSInstall, readOSRelease, aptCandidate, installExecCommand = origGOOS, origRead, origCand, origExec
	}()
	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) { return []byte("ID=debian\nVERSION_CODENAME=bookworm\n"), nil }
	aptCandidate = func(string) string { return "" }
	var shArgs []string
	installExecCommand = func(name string, args ...string) *exec.Cmd {
		if name == "sh" {
			shArgs = args
		}
		return exec.Command("true")
	}
	if _, err := RunInstall("8.4"); err != nil {
		t.Fatal(err)
	}
	if len(shArgs) != 2 || shArgs[0] != "-c" || !strings.Contains(shArgs[1], "> /etc/apt/sources.list.d/php.list") {
		t.Fatalf("sources.list step must run via sh -c, got %v", shArgs)
	}
}

func TestDistroBackportsFix(t *testing.T) {
	orig := readDistroChangelog
	defer func() { readDistroChangelog = orig }()
	readDistroChangelog = func(string) string { return "* SECURITY UPDATE: CVE-2026-109618 CVE-2026-109619" }
	if !distroBackportsFix("8.3") {
		t.Error("changelog naming both CVEs must count as fixed")
	}
	readDistroChangelog = func(string) string { return "* CVE-2026-109618 only" }
	if distroBackportsFix("8.3") {
		t.Error("one CVE is not enough")
	}
	readDistroChangelog = func(string) string { return "" }
	if distroBackportsFix("8.3") {
		t.Error("missing changelog must not clear the advisory")
	}
}
