package phpmanager

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Distro represents a detected Linux distribution.
type Distro struct {
	ID      string // "ubuntu", "debian", "centos", "fedora", "arch", "alpine"
	Version string // e.g. "22.04", "12"
	Name    string // e.g. "Ubuntu 22.04 LTS"
}

// DetectDistro reads /etc/os-release to identify the Linux distribution.
func DetectDistro() Distro {
	if runtime.GOOS != "linux" {
		return Distro{ID: runtime.GOOS, Name: runtime.GOOS}
	}
	data, err := os.ReadFile("/etc/os-release")
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
	}

	// Normalize version: "8.3.6" → "8.3", "8.3" stays "8.3"
	parts := strings.SplitN(phpVersion, ".", 3)
	short := phpVersion
	if len(parts) >= 2 {
		short = parts[0] + "." + parts[1]
	}

	switch d.ID {
	case "ubuntu", "debian":
		info.Packages = []string{
			fmt.Sprintf("php%s-cgi", short),
			fmt.Sprintf("php%s-fpm", short),
			fmt.Sprintf("php%s-cli", short),
			fmt.Sprintf("php%s-common", short),
			fmt.Sprintf("php%s-mysql", short),
			fmt.Sprintf("php%s-curl", short),
			fmt.Sprintf("php%s-gd", short),
			fmt.Sprintf("php%s-mbstring", short),
			fmt.Sprintf("php%s-xml", short),
			fmt.Sprintf("php%s-zip", short),
		}
		info.Commands = []string{
			"sudo add-apt-repository -y ppa:ondrej/php",
			"sudo apt update",
			fmt.Sprintf("sudo apt install -y %s", strings.Join(info.Packages, " ")),
		}
		info.Notes = "The ondrej/php PPA provides latest PHP versions for Ubuntu/Debian."

	case "centos", "rhel", "rocky", "alma":
		info.Packages = []string{
			fmt.Sprintf("php%s-php-cgi", strings.Replace(short, ".", "", 1)),
			fmt.Sprintf("php%s-php-fpm", strings.Replace(short, ".", "", 1)),
		}
		info.Commands = []string{
			"sudo dnf install -y epel-release",
			"sudo dnf install -y https://rpms.remirepo.net/enterprise/remi-release-$(rpm -E %{rhel}).rpm",
			fmt.Sprintf("sudo dnf module enable -y php:remi-%s", short),
			fmt.Sprintf("sudo dnf install -y php%s-php-cgi php%s-php-fpm php%s-php-mysqlnd php%s-php-gd php%s-php-mbstring",
				strings.Replace(short, ".", "", 1), strings.Replace(short, ".", "", 1),
				strings.Replace(short, ".", "", 1), strings.Replace(short, ".", "", 1),
				strings.Replace(short, ".", "", 1)),
		}
		info.Notes = "Uses the Remi repository for latest PHP versions."

	case "fedora":
		info.Commands = []string{
			fmt.Sprintf("sudo dnf install -y php-cgi php-fpm php-mysqlnd php-gd php-mbstring"),
		}
		info.Notes = "Fedora ships recent PHP versions by default."

	case "arch", "manjaro":
		info.Commands = []string{
			"sudo pacman -Sy php php-cgi php-fpm",
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

	return info
}

// RunInstall executes the install commands. Returns combined output and error.
func RunInstall(phpVersion string) (string, error) {
	info := GetInstallInfo(phpVersion)
	var output strings.Builder

	for _, cmdStr := range info.Commands {
		if strings.HasPrefix(cmdStr, "#") {
			output.WriteString(cmdStr + "\n")
			continue
		}
		output.WriteString(fmt.Sprintf("$ %s\n", cmdStr))

		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			continue
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		out, err := cmd.CombinedOutput()
		output.Write(out)
		output.WriteString("\n")
		if err != nil {
			return output.String(), fmt.Errorf("command failed: %s: %w", cmdStr, err)
		}
	}
	return output.String(), nil
}
