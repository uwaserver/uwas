package phpmanager

import (
	"os/exec"
	"testing"
)

// Regression F23401: the distro changelog exemption from the php2shell advisory
// applies only to binaries the package manager owns, not to a PHP built by hand
// outside /usr (or in /usr/local).
func TestProbeBackportExemptionOnlyForDistroBinaries(t *testing.T) {
	origCL := readDistroChangelog
	defer func() { readDistroChangelog = origCL }()
	readDistroChangelog = func(string) string { return "CVE-2026-109618 CVE-2026-109619" }

	advisoryFor := func(binary string) string {
		m := New(testLogger())
		m.execCommand = func(name string, args ...string) *exec.Cmd {
			switch args[0] {
			case "-v":
				return exec.Command("echo", "PHP 8.3.6 (cgi-fcgi) (built: Jan 1 2026)")
			case "-i":
				return exec.Command("echo", "Loaded Configuration File => /etc/uwas-test/php.ini")
			}
			return exec.Command("echo", "[PHP Modules]")
		}
		inst, err := m.probe(binary)
		if err != nil {
			t.Fatalf("probe %s: %v", binary, err)
		}
		return inst.Advisory
	}

	for _, bin := range []string{"/usr/bin/php-cgi8.3", "/usr/sbin/php-fpm8.3", "/usr/lib/cgi-bin/php8.3"} {
		if got := advisoryFor(bin); got != "" {
			t.Errorf("%s: distro build with backported fix must not be flagged, got %q", bin, got)
		}
	}
	for _, bin := range []string{
		"/usr/local/bin/php-cgi", "/usr/local/sbin/php-fpm", "/opt/php83/bin/php-cgi",
		"/usr/bin/../local/bin/php-cgi", "/home/dev/php/bin/php-cgi",
	} {
		if got := advisoryFor(bin); got == "" {
			t.Errorf("%s: not a distro binary, advisory must stay", bin)
		}
	}
}
