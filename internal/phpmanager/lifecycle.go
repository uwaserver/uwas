package phpmanager

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// installedPackages lists the installed distro packages of one PHP branch
// ("8.3" → php8.3-cli, php8.3-fpm, …). Overridden in tests.
var installedPackages = func(distroID, short string) []string {
	var out []byte
	var err error
	switch distroID {
	case "ubuntu", "debian":
		out, err = exec.Command("dpkg-query", "-W", "-f", "${db:Status-Abbrev} ${Package}\n", "php"+short+"-*").Output()
	case "alpine":
		out, err = exec.Command("apk", "info").Output()
	default:
		return nil
	}
	if err != nil && len(out) == 0 {
		return nil
	}
	var pkgs []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		name := f[len(f)-1]
		if distroID != "alpine" && (len(f) < 2 || f[0] != "ii") {
			continue // removed-but-configured packages are not installed
		}
		pkgs = append(pkgs, name)
	}
	return pkgs
}

// pkgNameRE bounds package names that reach an install/purge command line.
var pkgNameRE = regexp.MustCompile(`^php[0-9.]*-[a-z0-9][a-z0-9.+_-]*$`)

// branchRE accepts only a bare "N.N" branch; anything else (including "") would
// turn the "php<branch>-" package prefix into a glob over other packages.
var branchRE = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

func branchPackages(distroID, short string) []string {
	prefix := "php" + short + "-"
	if distroID == "alpine" {
		prefix = "php" + strings.Replace(short, ".", "", 1) + "-"
	}
	var out []string
	for _, p := range installedPackages(distroID, short) {
		if strings.HasPrefix(p, prefix) && pkgNameRE.MatchString(p) {
			out = append(out, p)
		}
	}
	return out
}

func shortOf(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return version
}

// GetUpdateInfo returns the commands that bring an installed PHP branch to the
// newest patch release its repository offers, then restart php-fpm if it runs.
func GetUpdateInfo(phpVersion string) InstallInfo {
	d := DetectDistro()
	short := shortOf(phpVersion)
	info := InstallInfo{Distro: d.Name, Version: short}
	nodot := strings.Replace(short, ".", "", 1)

	switch d.ID {
	case "ubuntu", "debian":
		pkgs := branchPackages(d.ID, short)
		if len(pkgs) == 0 {
			info.Commands = []string{fmt.Sprintf("# No installed php%s-* packages found; install PHP %s first.", short, short)}
			return info
		}
		info.Packages = pkgs
		info.Commands = []string{
			"apt-get update",
			"apt-get install -y --only-upgrade " + strings.Join(pkgs, " "),
			fmt.Sprintf("-systemctl try-restart php%s-fpm", short),
		}
		info.Notes = "Upgrades only the installed php" + short + "-* packages; php-fpm is restarted if it is running."
	case "centos", "rhel", "rocky", "alma":
		info.Commands = []string{
			fmt.Sprintf("dnf upgrade -y php%s-php*", nodot),
			fmt.Sprintf("-systemctl try-restart php%s-php-fpm", nodot),
		}
	case "fedora":
		info.Commands = []string{"dnf upgrade -y php*", "-systemctl try-restart php-fpm"}
	case "alpine":
		pkgs := branchPackages(d.ID, short)
		if len(pkgs) == 0 {
			info.Commands = []string{fmt.Sprintf("# No installed php%s-* packages found; install PHP %s first.", nodot, short)}
			return info
		}
		info.Packages = pkgs
		info.Commands = []string{"apk update", "apk add -u " + strings.Join(pkgs, " ")}
		info.Notes = "Restart php-fpm manually if it is managed by OpenRC."
	default:
		info.Commands = []string{"# Automatic PHP updates are not supported on this distribution; update the php packages with your package manager."}
	}
	return info
}

// GetUninstallInfo returns the commands that remove one PHP branch. purge also
// deletes its configuration (php.ini, pool files).
func GetUninstallInfo(phpVersion string, purge bool) InstallInfo {
	d := DetectDistro()
	short := shortOf(phpVersion)
	info := InstallInfo{Distro: d.Name, Version: short}
	nodot := strings.Replace(short, ".", "", 1)

	switch d.ID {
	case "ubuntu", "debian":
		pkgs := branchPackages(d.ID, short)
		if len(pkgs) == 0 {
			info.Commands = []string{fmt.Sprintf("# No installed php%s-* packages found; nothing to remove.", short)}
			return info
		}
		verb := "remove"
		if purge {
			verb = "purge"
		}
		info.Packages = pkgs
		info.Commands = []string{
			fmt.Sprintf("-systemctl stop php%s-fpm", short),
			fmt.Sprintf("apt-get %s -y %s", verb, strings.Join(pkgs, " ")),
			"apt-get autoremove -y",
		}
	case "centos", "rhel", "rocky", "alma":
		info.Commands = []string{
			fmt.Sprintf("-systemctl stop php%s-php-fpm", nodot),
			fmt.Sprintf("dnf remove -y php%s-php*", nodot),
		}
	case "alpine":
		pkgs := branchPackages(d.ID, short)
		if len(pkgs) == 0 {
			info.Commands = []string{fmt.Sprintf("# No installed php%s-* packages found; nothing to remove.", nodot)}
			return info
		}
		info.Packages = pkgs
		info.Commands = []string{"apk del " + strings.Join(pkgs, " ")}
	default:
		info.Commands = []string{"# Automatic PHP removal is not supported on this distribution; remove the php packages with your package manager."}
	}
	return info
}

// RunUpdate upgrades an installed PHP branch. EOL branches get no fixes, so
// they are refused instead of pretending to update.
func RunUpdate(phpVersion string) (string, error) {
	if !SupportedBranch(phpVersion) {
		return "", fmt.Errorf("PHP %s is end-of-life and receives no updates; install 8.2 or newer and move sites to it", phpVersion)
	}
	return runPlan(GetUpdateInfo(phpVersion).Commands)
}

// RunUninstall removes an installed PHP branch.
func RunUninstall(phpVersion string, purge bool) (string, error) {
	short := shortOf(phpVersion)
	if !branchRE.MatchString(short) {
		return "", fmt.Errorf("invalid PHP version %q", phpVersion)
	}
	return runPlan(GetUninstallInfo(phpVersion, purge).Commands)
}

// runPlan runs a plan but treats one made only of "#" comments as a failure:
// those plans mean "nothing to do here" or "unsupported", not success.
func runPlan(cmds []string) (string, error) {
	actionable := false
	for _, c := range cmds {
		if !strings.HasPrefix(c, "#") {
			actionable = true
			break
		}
	}
	if !actionable {
		return strings.Join(cmds, "\n") + "\n", fmt.Errorf("%s", strings.TrimSpace(strings.TrimPrefix(cmds[0], "#")))
	}
	return runCommands(cmds)
}

// Branch summarises one installed PHP branch (8.3) for update/uninstall
// decisions: the newest detected patch version, its security state and the
// domains still attached to it.
type Branch struct {
	Short    string   `json:"short"`
	Version  string   `json:"version"`
	EOL      bool     `json:"eol,omitempty"`
	Advisory string   `json:"advisory,omitempty"`
	Domains  []string `json:"domains,omitempty"`
}

// Branches returns one entry per detected PHP branch, FPM/CGI variants merged.
func (m *Manager) Branches() []Branch {
	var out []Branch
	idx := map[string]int{}
	for _, st := range m.Status() {
		short := shortVersion(st.Version)
		i, ok := idx[short]
		if !ok {
			idx[short] = len(out)
			out = append(out, Branch{Short: short, Version: st.Version})
			i = len(out) - 1
		}
		b := &out[i]
		b.EOL = b.EOL || st.EOL
		if b.Advisory == "" {
			b.Advisory = st.Advisory
		}
		for _, d := range st.Domains {
			seen := false
			for _, e := range b.Domains {
				if e == d {
					seen = true
					break
				}
			}
			if !seen {
				b.Domains = append(b.Domains, d)
			}
		}
	}
	return out
}

// SetInstallationsForTesting replaces the detected installations. It exists so
// other packages' tests can exercise handlers without PHP binaries on disk.
func (m *Manager) SetInstallationsForTesting(installs []PHPInstall) {
	m.mu.Lock()
	m.installations = installs
	m.mu.Unlock()
}
