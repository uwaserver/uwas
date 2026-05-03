package cloudflare

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Test hook so unit tests can stub out exec.
var execCommandFn = exec.Command

// CloudflaredInfo is what the dashboard renders for the "is it installed" badge.
type CloudflaredInfo struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

// DetectCloudflared returns whether the cloudflared binary is on PATH and,
// if so, its version string (e.g. "2024.10.0").
func DetectCloudflared() CloudflaredInfo {
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return CloudflaredInfo{Installed: false}
	}
	info := CloudflaredInfo{Installed: true, Path: path}

	out, err := execCommandFn(path, "--version").CombinedOutput()
	if err == nil {
		// Output looks like: "cloudflared version 2024.10.0 (built 2024-10-09-1820 UTC)"
		parts := strings.Fields(strings.TrimSpace(string(out)))
		for i, p := range parts {
			if p == "version" && i+1 < len(parts) {
				info.Version = parts[i+1]
				break
			}
		}
	}
	return info
}

// InstallCloudflared installs the cloudflared binary using the system package
// manager (Linux only). Returns the post-install info.
func InstallCloudflared() (CloudflaredInfo, error) {
	if runtime.GOOS != "linux" {
		return CloudflaredInfo{}, fmt.Errorf("automatic install only supported on Linux (current: %s) — see https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/", runtime.GOOS)
	}

	// Try the official Cloudflare apt repo first (stable, signed). Fallback to
	// the pre-built .deb that mirrors what the docs install.
	steps := [][]string{
		// Add Cloudflare GPG key + apt source
		{"sh", "-c", `mkdir -p /usr/share/keyrings && curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null`},
		{"sh", "-c", `echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs 2>/dev/null || echo noble) main" | tee /etc/apt/sources.list.d/cloudflared.list >/dev/null`},
		{"apt-get", "update", "-y"},
		{"apt-get", "install", "-y", "cloudflared"},
	}

	var lastErr error
	for _, step := range steps {
		out, err := execCommandFn(step[0], step[1:]...).CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("%s failed: %w; output: %s", strings.Join(step, " "), err, strings.TrimSpace(string(out)))
		}
	}

	info := DetectCloudflared()
	if !info.Installed {
		if lastErr != nil {
			return info, lastErr
		}
		return info, errors.New("cloudflared install completed but binary still not on PATH")
	}
	return info, nil
}
