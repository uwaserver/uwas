package phpmanager

import (
	"os/exec"
	"strings"
	"testing"
)

func withPkgs(id string, pkgs []string, fn func()) {
	origGOOS, origRead, origPk := runtimeGOOSInstall, readOSRelease, installedPackages
	defer func() { runtimeGOOSInstall, readOSRelease, installedPackages = origGOOS, origRead, origPk }()
	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte("ID=" + id + "\nVERSION_ID=9\nVERSION_CODENAME=noble\nPRETTY_NAME=\"Test\"\n"), nil
	}
	installedPackages = func(string, string) []string { return pkgs }
	fn()
}

func TestUpdateInfoDebianUpgradesOnlyInstalledPackages(t *testing.T) {
	pkgs := []string{"php8.3-fpm", "php8.3-cli", "php8.3-common", "php8.2-cli", "php8.3-bad;rm -rf", "php-imagick"}
	withPkgs("ubuntu", pkgs, func() {
		all := strings.Join(GetUpdateInfo("8.3.6").Commands, "\n")
		if !strings.Contains(all, "apt-get install -y --only-upgrade php8.3-fpm php8.3-cli php8.3-common") {
			t.Fatalf("missing upgrade of installed packages:\n%s", all)
		}
		for _, bad := range []string{"php8.2-cli", "rm -rf", "php-imagick"} {
			if strings.Contains(all, bad) {
				t.Fatalf("plan must not contain %q:\n%s", bad, all)
			}
		}
		if !strings.Contains(all, "-systemctl try-restart php8.3-fpm") {
			t.Fatalf("php-fpm restart step missing:\n%s", all)
		}
	})
}

func TestUpdateInfoNothingInstalledIsCommentOnly(t *testing.T) {
	withPkgs("debian", nil, func() {
		for _, c := range GetUpdateInfo("8.4").Commands {
			if !strings.HasPrefix(c, "#") {
				t.Fatalf("expected comments only, got %q", c)
			}
		}
		if _, err := RunUpdate("8.4"); err == nil {
			t.Fatal("RunUpdate with nothing installed must fail")
		}
	})
}

func TestRunUpdateRefusesEOL(t *testing.T) {
	if _, err := RunUpdate("8.1"); err == nil || !strings.Contains(err.Error(), "end-of-life") {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallInfoPurgeVsRemove(t *testing.T) {
	withPkgs("ubuntu", []string{"php8.3-fpm", "php8.3-cli"}, func() {
		rm := strings.Join(GetUninstallInfo("8.3", false).Commands, "\n")
		pg := strings.Join(GetUninstallInfo("8.3", true).Commands, "\n")
		if !strings.Contains(rm, "apt-get remove -y php8.3-fpm php8.3-cli") || strings.Contains(rm, "purge") {
			t.Fatalf("remove plan wrong:\n%s", rm)
		}
		if !strings.Contains(pg, "apt-get purge -y php8.3-fpm php8.3-cli") {
			t.Fatalf("purge plan wrong:\n%s", pg)
		}
		if !strings.Contains(rm, "-systemctl stop php8.3-fpm") {
			t.Fatalf("service stop must precede removal:\n%s", rm)
		}
	})
}

func TestRunUninstallRejectsBadVersion(t *testing.T) {
	for _, v := range []string{"", "../x", "8.3;rm", "latest"} {
		if _, err := RunUninstall(v, false); err == nil {
			t.Errorf("RunUninstall(%q) must fail", v)
		}
	}
}

func TestRunCommandsDashPrefixIgnoresFailure(t *testing.T) {
	orig := installExecCommand
	defer func() { installExecCommand = orig }()
	var ran []string
	installExecCommand = func(name string, args ...string) *exec.Cmd {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if name == "systemctl" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	out, err := runCommands([]string{"-systemctl stop php8.3-fpm", "apt-get remove -y php8.3-cli"})
	if err != nil {
		t.Fatalf("a '-' command failing must not abort: %v", err)
	}
	if len(ran) != 2 || !strings.Contains(out, "(ignored:") {
		t.Fatalf("ran=%v out=%q", ran, out)
	}
	installExecCommand = func(string, ...string) *exec.Cmd { return exec.Command("false") }
	if _, err := runCommands([]string{"apt-get remove -y x"}); err == nil {
		t.Fatal("a plain failing command must abort")
	}
}
