package phpmanager

import (
	"os/exec"
	"strings"
	"testing"
)

// RunUninstall/RunUpdate are exported; a version that is not N.N must never
// become a "php-*" package glob (Debian metapackages php-common, php-cli, …).
func TestRunUninstallRejectsNonBranchVersion(t *testing.T) {
	origExec := installExecCommand
	defer func() { installExecCommand = origExec }()
	var ran []string
	installExecCommand = func(name string, args ...string) *exec.Cmd {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return exec.Command("true")
	}
	pkgs := []string{"php-common", "php-cli", "php8.3-fpm"}
	for _, v := range []string{"", "8", "..", "8..", "x.y", "8.3-x"} {
		ran = nil
		withPkgs("ubuntu", pkgs, func() {
			if _, err := RunUninstall(v, true); err == nil || len(ran) != 0 {
				t.Errorf("RunUninstall(%q): err=%v ran=%v; want rejection without running anything", v, err, ran)
			}
		})
	}
	// Control: a real branch still removes its packages.
	ran = nil
	withPkgs("ubuntu", pkgs, func() {
		if _, err := RunUninstall("8.3", true); err != nil || len(ran) == 0 {
			t.Errorf("RunUninstall(8.3): err=%v ran=%v", err, ran)
		}
	})
}

func TestSuryKeyringNotStagedInWorldWritableDir(t *testing.T) {
	withApt("ubuntu", "noble", func(string) string { return "" }, func() {
		all := strings.Join(GetInstallInfo("8.4").Commands, "\n")
		if strings.Contains(all, "/tmp/") || strings.Contains(all, "/var/tmp/") {
			t.Errorf("keyring .deb is downloaded and installed as root through a world-writable dir:\n%s", all)
		}
		if !strings.Contains(all, "debsuryorg-archive-keyring.deb") {
			t.Errorf("keyring step missing:\n%s", all)
		}
	})
}
