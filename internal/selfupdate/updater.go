// Package selfupdate provides UWAS self-update functionality.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner = "uwaserver"
	repoName  = "uwas"
)

// Testable hooks
var (
	githubAPIBase  = "https://api.github.com"
	httpClientFn   = func(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }
	osExecutableFn = os.Executable
	osRenameFn     = os.Rename
	osRemoveFn     = os.Remove
	osChmodFn      = os.Chmod
	evalSymlinksFn = filepath.EvalSymlinks
	osCreateTempFn = os.CreateTemp
	runtimeGOOS    = runtime.GOOS
	runtimeGOARCH  = runtime.GOARCH
)

// ReleaseInfo contains version information.
type ReleaseInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	UpdateAvail    bool   `json:"update_available"`
	ReleaseURL     string `json:"release_url,omitempty"`
	PublishedAt    string `json:"published_at,omitempty"`
	ReleaseNotes   string `json:"release_notes,omitempty"`
	DownloadURL    string `json:"download_url,omitempty"`
}

// CheckUpdate checks GitHub for a newer release.
func CheckUpdate(currentVersion string) (*ReleaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPIBase, repoOwner, repoName)

	client := httpClientFn(10 * time.Second)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(currentVersion, "v")

	info := &ReleaseInfo{
		CurrentVersion: current,
		LatestVersion:  latest,
		UpdateAvail:    latest != current && current != "dev",
		ReleaseURL:     release.HTMLURL,
		PublishedAt:    release.PublishedAt,
		ReleaseNotes:   release.Body,
	}

	// Find matching asset for this platform
	wantName := fmt.Sprintf("uwas-%s-%s", runtimeGOOS, runtimeGOARCH)
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, wantName) {
			info.DownloadURL = asset.BrowserDownloadURL
			break
		}
	}

	return info, nil
}

// Update downloads and replaces the current binary.
func Update(downloadURL string) error {
	if downloadURL == "" {
		return fmt.Errorf("no download URL for %s/%s", runtimeGOOS, runtimeGOARCH)
	}

	// Download to temp file
	client := httpClientFn(5 * time.Minute)
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	tmp, err := osCreateTempFn("", "uwas-update-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer osRemoveFn(tmp.Name())

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	tmp.Close()

	// Make executable
	if err := osChmodFn(tmp.Name(), 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Replace current binary
	exe, err := osExecutableFn()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	exe, _ = evalSymlinksFn(exe)

	// Backup current binary
	backup := exe + ".bak"
	osRemoveFn(backup)
	if err := osRenameFn(exe, backup); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := osRenameFn(tmp.Name(), exe); err != nil {
		// Restore backup
		osRenameFn(backup, exe)
		return fmt.Errorf("replace binary: %w", err)
	}

	osRemoveFn(backup)
	return nil
}
