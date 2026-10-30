package phpmanager

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// Testable hooks for install.go — overridden in tests.
var (
	// runtimeGOOSInstall is the OS identifier used by DetectDistro.
	runtimeGOOSInstall = runtime.GOOS
	// readOSRelease reads the os-release file. Defaults to reading /etc/os-release.
	readOSRelease = func() ([]byte, error) { return os.ReadFile("/etc/os-release") }
	// installExecCommand is the function used by RunInstall to create exec.Cmd objects.
	installExecCommand = exec.Command
	// aptCandidate returns the version apt would install for pkg, or "" when no
	// configured repository provides it.
	aptCandidate = func(pkg string) string {
		out, err := exec.Command("apt-cache", "policy", pkg).Output()
		if err != nil {
			return ""
		}
		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Candidate:"); ok {
				v = strings.TrimSpace(v)
				if v == "(none)" {
					return ""
				}
				return v
			}
		}
		return ""
	}
	// readDistroChangelog returns the Debian changelog of the phpX.Y-common
	// package, or "" when unavailable. Debian/Ubuntu backport fixes without
	// changing the upstream version, so the changelog is the evidence.
	readDistroChangelog = func(short string) string {
		f, err := os.Open("/usr/share/doc/php" + short + "-common/changelog.Debian.gz")
		if err != nil {
			return ""
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return ""
		}
		b, _ := io.ReadAll(io.LimitReader(zr, 8<<20))
		return string(b)
	}
)

// suryCodenames are the releases packages.sury.org/php publishes for.
var suryCodenames = map[string]bool{
	"bullseye": true, "bookworm": true, "trixie": true,
	"jammy": true, "noble": true, "resolute": true,
}

var codenameRE = regexp.MustCompile(`^[a-z]+$`)

// Distro represents a detected Linux distribution.
type Distro struct {
	ID      string // "ubuntu", "debian", "centos", "fedora", "arch", "alpine"
	Version string // e.g. "22.04", "12"
	Name    string // e.g. "Ubuntu 22.04 LTS"
	// Codename is VERSION_CODENAME, e.g. "noble"; "" when absent or not a plain word.
	Codename string
}

// DetectDistro reads /etc/os-release to identify the Linux distribution.
func DetectDistro() Distro {
	if runtimeGOOSInstall != "linux" {
		return Distro{ID: runtimeGOOSInstall, Name: runtimeGOOSInstall}
	}
	data, err := readOSRelease()
	if err != nil {
		return Distro{ID: "unknown"}
	}
	d := Distro{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "ID":
			d.ID = v
		case "VERSION_ID":
			d.Version = v
		case "PRETTY_NAME":
			d.Name = v
		case "VERSION_CODENAME":
			if codenameRE.MatchString(v) {
				d.Codename = v
			}
		}
	}
	return d
}

// InstallInfo contains installation instructions for a PHP version.
type InstallInfo struct {
	Distro   string   `json:"distro"`
	Version  string   `json:"version"`
	Commands []string `json:"commands"`
	Packages []string `json:"packages"`
	Notes    string   `json:"notes,omitempty"`
}

// GetInstallInfo returns OS-specific install instructions for a PHP version.
func GetInstallInfo(phpVersion string) InstallInfo {
	d := DetectDistro()
	info := InstallInfo{
		Distro:  d.Name,
		Version: phpVersion,
		// Non-nil so the JSON is [] rather than null on the distributions
		// whose plan has no package list (the dashboard types it string[]).
		Packages: []string{},
	}

	// Normalize version: "8.3.6" → "8.3", "8.3" stays "8.3"
	parts := strings.SplitN(phpVersion, ".", 3)
	short := phpVersion
	if len(parts) >= 2 {
		short = parts[0] + "." + parts[1]
	}

	switch d.ID {
	case "ubuntu", "debian":
		pkg := func(name string) string { return fmt.Sprintf("php%s-%s", short, name) }
		base := []string{"cgi", "fpm", "cli", "common", "mysql", "curl", "gd", "intl", "mbstring", "xml", "zip"}
		for _, b := range base {
			info.Packages = append(info.Packages, pkg(b))
		}
		install := "apt-get install -y " + strings.Join(info.Packages, " ")
		switch {
		case aptCandidate(pkg("fpm")) != "":
			// The distribution archive (or an already-configured repo) has it.
			info.Commands = []string{"apt-get update", install}
			info.Notes = "Installed from the distribution's own archive; the distro security team backports fixes without changing the upstream version number."
		case suryCodenames[d.Codename]:
			// Debian-maintained packages.sury.org (successor of the ondrej PPA).
			info.Packages = append(info.Packages, pkg("imagick"))
			install = "apt-get install -y " + strings.Join(info.Packages, " ")
			info.Commands = []string{
				"apt-get update",
				"apt-get install -y ca-certificates curl",
				"curl -sSfLo /run/debsuryorg-archive-keyring.deb https://packages.sury.org/debsuryorg-archive-keyring.deb",
				"dpkg -i /run/debsuryorg-archive-keyring.deb",
				fmt.Sprintf(`sh -c echo "deb [signed-by=/usr/share/keyrings/debsuryorg-archive-keyring.gpg] https://packages.sury.org/php/ %s main" > /etc/apt/sources.list.d/php.list`, d.Codename),
				"apt-get update",
				install,
			}
			info.Notes = fmt.Sprintf("PHP %s is not in the %s archive, so the packages.sury.org repository (maintained by the Debian PHP maintainer, replaces the ondrej PPA) is added.", short, d.Name)
		default:
			info.Commands = []string{
				"# PHP " + short + " is not in this distribution's archive and packages.sury.org does not cover it.",
				"# Use a release that ships it or build PHP from source.",
			}
		}

	case "centos", "rhel", "rocky", "alma":
		// RunInstall executes commands without a shell (strings.Fields), so
		// `$(rpm -E %{rhel})` was never expanded — and its internal spaces even
		// split the URL across args, so the remi-release install always failed.
		// Substitute the RHEL major version from os-release's VERSION_ID instead.
		rhelMajor := d.Version
		if i := strings.IndexByte(rhelMajor, '.'); i >= 0 {
			rhelMajor = rhelMajor[:i]
		}
		info.Packages = []string{
			fmt.Sprintf("php%s-php-cgi", strings.Replace(short, ".", "", 1)),
			fmt.Sprintf("php%s-php-fpm", strings.Replace(short, ".", "", 1)),
		}
		info.Commands = []string{
			"dnf install -y epel-release",
			fmt.Sprintf("dnf install -y https://rpms.remirepo.net/enterprise/remi-release-%s.rpm", rhelMajor),
			fmt.Sprintf("dnf module enable -y php:remi-%s", short),
			fmt.Sprintf("dnf install -y php%s-php-cgi php%s-php-fpm php%s-php-mysqlnd php%s-php-gd php%s-php-mbstring",
				strings.Replace(short, ".", "", 1), strings.Replace(short, ".", "", 1),
				strings.Replace(short, ".", "", 1), strings.Replace(short, ".", "", 1),
				strings.Replace(short, ".", "", 1)),
		}
		info.Notes = "Uses the Remi repository for latest PHP versions."

	case "fedora":
		info.Commands = []string{
			"dnf install -y php-cgi php-fpm php-mysqlnd php-gd php-mbstring",
		}
		info.Notes = "Fedora ships recent PHP versions by default."

	case "arch", "manjaro":
		info.Commands = []string{
			"pacman -Sy php php-cgi php-fpm",
		}

	case "alpine":
		info.Packages = []string{
			fmt.Sprintf("php%s-cgi", strings.Replace(short, ".", "", 1)),
			fmt.Sprintf("php%s-fpm", strings.Replace(short, ".", "", 1)),
		}
		info.Commands = []string{
			fmt.Sprintf("apk add php%s-cgi php%s-fpm php%s-mysqli php%s-curl php%s-gd php%s-mbstring",
				strings.Replace(short, ".", "", 1), strings.Replace(short, ".", "", 1),
				strings.Replace(short, ".", "", 1), strings.Replace(short, ".", "", 1),
				strings.Replace(short, ".", "", 1), strings.Replace(short, ".", "", 1)),
		}

	default:
		info.Commands = []string{
			"# Could not detect your distribution.",
			fmt.Sprintf("# Install php%s-cgi or php%s-fpm using your package manager.", short, short),
		}
		info.Notes = "UWAS needs php-cgi (FastCGI) or php-fpm to serve PHP sites."
	}

	if supported, warn := Advisory(short + ".0"); !supported {
		info.Notes = strings.TrimSpace(warn + ". " + info.Notes)
	}

	return info
}

// RunInstall executes the install commands. Returns combined output and error.
func RunInstall(phpVersion string) (string, error) {
	if !SupportedBranch(phpVersion) {
		return "", fmt.Errorf("PHP %s is end-of-life or invalid; only 8.2 and newer can be installed", phpVersion)
	}
	return runCommands(GetInstallInfo(phpVersion).Commands)
}

// runCommands executes a command plan without a shell. Lines starting with
// "#" are printed only; a leading "-" lets that one command fail (systemd
// style) and "sh -c " hands the rest to a shell for redirections.
func runCommands(cmds []string) (string, error) {
	var output strings.Builder

	for _, cmdStr := range cmds {
		if strings.HasPrefix(cmdStr, "#") {
			output.WriteString(cmdStr + "\n")
			continue
		}

		// Strip sudo — UWAS runs as root, sudo without TTY blocks for password
		cmdStr = strings.TrimPrefix(cmdStr, "sudo ")
		allowFail := false
		if rest, ok := strings.CutPrefix(cmdStr, "-"); ok {
			allowFail, cmdStr = true, rest
		}

		output.WriteString(fmt.Sprintf("$ %s\n", cmdStr))

		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			continue
		}
		cmd := installExecCommand(parts[0], parts[1:]...)
		if rest, ok := strings.CutPrefix(cmdStr, "sh -c "); ok {
			// Generated by GetInstallInfo only (no user input); needs a shell
			// for the redirection.
			cmd = installExecCommand("sh", "-c", rest)
		}
		// Prevent interactive prompts when running from API (no TTY):
		// - DEBIAN_FRONTEND: suppresses apt dialog prompts
		// - NEEDRESTART_MODE: auto-restart services without asking
		// - APT_LISTCHANGES_FRONTEND: skip changelog display
		// - DEBIAN_PRIORITY: skip non-critical debconf questions
		cmd.Env = append(os.Environ(),
			"DEBIAN_FRONTEND=noninteractive",
			"NEEDRESTART_MODE=a",
			"APT_LISTCHANGES_FRONTEND=none",
			"DEBIAN_PRIORITY=critical",
		)
		out, err := cmd.CombinedOutput()
		output.Write(out)
		output.WriteString("\n")
		if err != nil {
			if allowFail {
				output.WriteString(fmt.Sprintf("(ignored: %v)\n", err))
				continue
			}
			return output.String(), fmt.Errorf("command failed: %s: %w", cmdStr, err)
		}
	}
	return output.String(), nil
}

// distroBackportsFix reports whether the Debian-family changelog for a PHP
// branch lists both php2shell CVEs, i.e. the distro already carries the fix
// even though the upstream version number is older.
func distroBackportsFix(short string) bool {
	cl := readDistroChangelog(short)
	return cl != "" && strings.Contains(cl, "CVE-2026-109618") && strings.Contains(cl, "CVE-2026-109619")
}
