package selfupdate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// saveHooks saves all package-level hooks and returns a restore function.
func saveHooks(t *testing.T) {
	t.Helper()
	origGithubAPIBase := githubAPIBase
	origHttpClientFn := httpClientFn
	origOsExecutableFn := osExecutableFn
	origOsRenameFn := osRenameFn
	origOsRemoveFn := osRemoveFn
	origOsChmodFn := osChmodFn
	origEvalSymlinksFn := evalSymlinksFn
	origOsCreateTempFn := osCreateTempFn
	origRuntimeGOOS := runtimeGOOS
	origRuntimeGOARCH := runtimeGOARCH
	origIsTrustedDownloadURL := isTrustedDownloadURL
	// Allow any URL in tests (test servers use localhost).
	isTrustedDownloadURL = func(string) bool { return true }
	t.Cleanup(func() {
		githubAPIBase = origGithubAPIBase
		httpClientFn = origHttpClientFn
		osExecutableFn = origOsExecutableFn
		osRenameFn = origOsRenameFn
		osRemoveFn = origOsRemoveFn
		osChmodFn = origOsChmodFn
		evalSymlinksFn = origEvalSymlinksFn
		osCreateTempFn = origOsCreateTempFn
		runtimeGOOS = origRuntimeGOOS
		runtimeGOARCH = origRuntimeGOARCH
		isTrustedDownloadURL = origIsTrustedDownloadURL
	})
}

// githubRelease is a helper to build JSON responses matching the GitHub API format.
type githubRelease struct {
	TagName     string        `json:"tag_name"`
	HTMLURL     string        `json:"html_url"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func newGitHubServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// ---------- CheckUpdate tests ----------

func TestCheckUpdate_UpdateAvailable(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{
			TagName:     "v1.2.0",
			HTMLURL:     "https://github.com/uwaserver/uwas/releases/tag/v1.2.0",
			Body:        "Release notes here",
			PublishedAt: "2024-01-01T00:00:00Z",
			Assets: []githubAsset{
				{Name: "uwas-linux-amd64", BrowserDownloadURL: "https://example.com/uwas-linux-amd64"},
			},
		}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL

	info, err := CheckUpdate("v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.UpdateAvail {
		t.Error("expected update available")
	}
	if info.LatestVersion != "1.2.0" {
		t.Errorf("latest = %q, want 1.2.0", info.LatestVersion)
	}
	if info.CurrentVersion != "1.0.0" {
		t.Errorf("current = %q, want 1.0.0", info.CurrentVersion)
	}
	if info.ReleaseURL != "https://github.com/uwaserver/uwas/releases/tag/v1.2.0" {
		t.Errorf("release URL = %q", info.ReleaseURL)
	}
	if info.ReleaseNotes != "Release notes here" {
		t.Errorf("release notes = %q", info.ReleaseNotes)
	}
	if info.PublishedAt != "2024-01-01T00:00:00Z" {
		t.Errorf("published_at = %q", info.PublishedAt)
	}
	if info.DownloadURL != "https://example.com/uwas-linux-amd64" {
		t.Errorf("download URL = %q", info.DownloadURL)
	}
}

func TestCheckUpdate_NoUpdate(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{
			TagName: "v1.0.0",
			HTMLURL: "https://github.com/uwaserver/uwas/releases/tag/v1.0.0",
			Assets: []githubAsset{
				{Name: "uwas-linux-amd64", BrowserDownloadURL: "https://example.com/uwas-linux-amd64"},
			},
		}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL

	info, err := CheckUpdate("v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.UpdateAvail {
		t.Error("expected no update available when versions match")
	}
	if info.CurrentVersion != "1.0.0" {
		t.Errorf("current = %q, want 1.0.0", info.CurrentVersion)
	}
	if info.LatestVersion != "1.0.0" {
		t.Errorf("latest = %q, want 1.0.0", info.LatestVersion)
	}
}

func TestCheckUpdate_DevVersion(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{
			TagName: "v2.0.0",
			HTMLURL: "https://github.com/uwaserver/uwas/releases/tag/v2.0.0",
			Assets:  []githubAsset{},
		}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL

	info, err := CheckUpdate("dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.UpdateAvail {
		t.Error("dev version should not show update available")
	}
	if info.CurrentVersion != "dev" {
		t.Errorf("current = %q, want dev", info.CurrentVersion)
	}
	if info.LatestVersion != "2.0.0" {
		t.Errorf("latest = %q, want 2.0.0", info.LatestVersion)
	}
}

func TestCheckUpdate_NetworkError(t *testing.T) {
	saveHooks(t)
	githubAPIBase = "http://127.0.0.1:1" // nothing listening

	_, err := CheckUpdate("v1.0.0")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "check update") {
		t.Errorf("error = %q, want 'check update' prefix", err.Error())
	}
}

func TestCheckUpdate_InvalidJSON(t *testing.T) {
	saveHooks(t)

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("this is not json"))
	})
	githubAPIBase = srv.URL

	_, err := CheckUpdate("v1.0.0")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse release") {
		t.Errorf("error = %q, want 'parse release' prefix", err.Error())
	}
}

func TestCheckUpdate_Non200Status(t *testing.T) {
	saveHooks(t)

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"message": "Not Found"}`))
	})
	githubAPIBase = srv.URL

	_, err := CheckUpdate("v1.0.0")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "GitHub API returned 404") {
		t.Errorf("error = %q, want 'GitHub API returned 404'", err.Error())
	}
}

func TestCheckUpdate_NoMatchingAsset(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "darwin"
	runtimeGOARCH = "arm64"

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{
			TagName: "v1.2.0",
			HTMLURL: "https://github.com/uwaserver/uwas/releases/tag/v1.2.0",
			Assets: []githubAsset{
				{Name: "uwas-linux-amd64", BrowserDownloadURL: "https://example.com/uwas-linux-amd64"},
				{Name: "uwas-linux-arm64", BrowserDownloadURL: "https://example.com/uwas-linux-arm64"},
			},
		}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL

	info, err := CheckUpdate("v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.DownloadURL != "" {
		t.Errorf("expected empty download URL for darwin/arm64, got %q", info.DownloadURL)
	}
	if !info.UpdateAvail {
		t.Error("expected update available even without matching asset")
	}
}

func TestCheckUpdate_EmptyTagName(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{
			TagName: "",
			HTMLURL: "https://github.com/uwaserver/uwas/releases",
			Assets:  []githubAsset{},
		}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL

	info, err := CheckUpdate("v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty tag -> latest="" which differs from "1.0.0" -> update available
	if info.LatestVersion != "" {
		t.Errorf("latest = %q, want empty", info.LatestVersion)
	}
	if !info.UpdateAvail {
		t.Error("expected update available when latest is empty (differs from current)")
	}
}

func TestCheckUpdate_VersionComparison(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	tests := []struct {
		name           string
		currentVersion string
		tagName        string
		wantAvail      bool
	}{
		{"same without v prefix", "1.0.0", "v1.0.0", false},
		{"same with v prefix", "v1.0.0", "v1.0.0", false},
		{"different versions", "1.0.0", "v1.1.0", true},
		{"dev version", "dev", "v2.0.0", false},
		{"newer local (downgrade scenario)", "2.0.0", "v1.0.0", true}, // simple string compare, not semver
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
				rel := githubRelease{
					TagName: tt.tagName,
					Assets:  []githubAsset{},
				}
				json.NewEncoder(w).Encode(rel)
			})
			githubAPIBase = srv.URL

			info, err := CheckUpdate(tt.currentVersion)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.UpdateAvail != tt.wantAvail {
				t.Errorf("UpdateAvail = %v, want %v", info.UpdateAvail, tt.wantAvail)
			}
		})
	}
}

func TestCheckUpdate_MultipleAssets(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "arm64"

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{
			TagName: "v1.5.0",
			HTMLURL: "https://github.com/uwaserver/uwas/releases/tag/v1.5.0",
			Assets: []githubAsset{
				{Name: "uwas-linux-amd64", BrowserDownloadURL: "https://example.com/uwas-linux-amd64"},
				{Name: "uwas-darwin-amd64", BrowserDownloadURL: "https://example.com/uwas-darwin-amd64"},
				{Name: "uwas-linux-arm64", BrowserDownloadURL: "https://example.com/uwas-linux-arm64"},
				{Name: "uwas-darwin-arm64", BrowserDownloadURL: "https://example.com/uwas-darwin-arm64"},
			},
		}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL

	info, err := CheckUpdate("v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.DownloadURL != "https://example.com/uwas-linux-arm64" {
		t.Errorf("download URL = %q, want linux-arm64 URL", info.DownloadURL)
	}
}

// ---------- Update tests ----------

func TestUpdate_Success(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	// Create a fake "current" binary
	exePath := filepath.Join(tmpDir, "uwas")
	if err := os.WriteFile(exePath, []byte("old-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	osExecutableFn = func() (string, error) { return exePath, nil }
	evalSymlinksFn = func(p string) (string, error) { return p, nil }

	// Serve a fake binary download
	binaryContent := []byte("new-binary-content")
	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(binaryContent)
	})

	err := Update(srv.URL + "/download")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the binary was replaced
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read new binary: %v", err)
	}
	if string(got) != "new-binary-content" {
		t.Errorf("binary content = %q, want %q", got, "new-binary-content")
	}

	// Verify backup was cleaned up
	if _, err := os.Stat(exePath + ".bak"); !os.IsNotExist(err) {
		t.Error("backup file should have been removed")
	}
}

func TestUpdate_EmptyURL(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	err := Update("")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "no download URL") {
		t.Errorf("error = %q, want 'no download URL'", err.Error())
	}
}

func TestUpdate_DownloadError(t *testing.T) {
	saveHooks(t)

	err := Update("http://127.0.0.1:1/nonexistent")
	if err == nil {
		t.Fatal("expected error for unreachable download")
	}
	if !strings.Contains(err.Error(), "download") {
		t.Errorf("error = %q, want 'download' prefix", err.Error())
	}
}

func TestUpdate_CreateTempFailure(t *testing.T) {
	saveHooks(t)

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("binary"))
	})

	osCreateTempFn = func(dir, pattern string) (*os.File, error) {
		return nil, fmt.Errorf("injected createtemp error")
	}

	err := Update(srv.URL + "/download")
	if err == nil {
		t.Fatal("expected error for CreateTemp failure")
	}
	if !strings.Contains(err.Error(), "create temp") {
		t.Errorf("error = %q, want 'create temp'", err.Error())
	}
}

func TestUpdate_WriteFailure(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("binary-data"))
	})

	// Return a file that has been pre-closed so io.Copy fails on write
	osCreateTempFn = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(tmpDir, pattern)
		if err != nil {
			return nil, err
		}
		f.Close() // close it so io.Copy will fail
		// Reopen read-only so io.Copy fails on write
		rf, err := os.Open(f.Name())
		if err != nil {
			return nil, err
		}
		return rf, nil
	}

	err := Update(srv.URL + "/download")
	if err == nil {
		t.Fatal("expected error for write failure")
	}
	if !strings.Contains(err.Error(), "write") {
		t.Errorf("error = %q, want 'write' prefix", err.Error())
	}
}

func TestUpdate_ChmodFailure(t *testing.T) {
	saveHooks(t)

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("binary"))
	})

	osChmodFn = func(name string, mode os.FileMode) error {
		return fmt.Errorf("injected chmod error")
	}

	err := Update(srv.URL + "/download")
	if err == nil {
		t.Fatal("expected error for chmod failure")
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Errorf("error = %q, want 'chmod'", err.Error())
	}
}

func TestUpdate_ExecutableError(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("binary"))
	})

	// Let CreateTemp work in our temp dir
	osCreateTempFn = func(dir, pattern string) (*os.File, error) {
		return os.CreateTemp(tmpDir, pattern)
	}
	osExecutableFn = func() (string, error) {
		return "", fmt.Errorf("injected executable error")
	}

	err := Update(srv.URL + "/download")
	if err == nil {
		t.Fatal("expected error for os.Executable failure")
	}
	if !strings.Contains(err.Error(), "find executable") {
		t.Errorf("error = %q, want 'find executable'", err.Error())
	}
}

func TestUpdate_RenameFailure_BackupPhase(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	exePath := filepath.Join(tmpDir, "uwas")
	if err := os.WriteFile(exePath, []byte("current-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	osExecutableFn = func() (string, error) { return exePath, nil }
	evalSymlinksFn = func(p string) (string, error) { return p, nil }

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("new-binary"))
	})

	// Fail on the first rename (backup current binary)
	osRenameFn = func(oldpath, newpath string) error {
		return fmt.Errorf("injected backup rename error")
	}

	err := Update(srv.URL + "/download")
	if err == nil {
		t.Fatal("expected error for rename failure")
	}
	if !strings.Contains(err.Error(), "backup current binary") {
		t.Errorf("error = %q, want 'backup current binary'", err.Error())
	}

	// Original binary should still be intact
	got, _ := os.ReadFile(exePath)
	if string(got) != "current-binary" {
		t.Errorf("original binary was modified: %q", got)
	}
}

func TestUpdate_RenameFailure_ReplacePhase_BackupRestored(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	exePath := filepath.Join(tmpDir, "uwas")
	if err := os.WriteFile(exePath, []byte("current-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	osExecutableFn = func() (string, error) { return exePath, nil }
	evalSymlinksFn = func(p string) (string, error) { return p, nil }

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("new-binary"))
	})

	// Track rename calls: allow first (backup), fail second (replace)
	renameCallCount := 0
	osRenameFn = func(oldpath, newpath string) error {
		renameCallCount++
		switch renameCallCount {
		case 1:
			// First call: backup exe -> exe.bak (allow it)
			return os.Rename(oldpath, newpath)
		case 2:
			// Second call: tmp -> exe (fail it)
			return fmt.Errorf("injected replace rename error")
		case 3:
			// Third call: restore backup exe.bak -> exe
			return os.Rename(oldpath, newpath)
		default:
			return os.Rename(oldpath, newpath)
		}
	}

	err := Update(srv.URL + "/download")
	if err == nil {
		t.Fatal("expected error for replace rename failure")
	}
	if !strings.Contains(err.Error(), "replace binary") {
		t.Errorf("error = %q, want 'replace binary'", err.Error())
	}

	// Backup should have been restored
	got, readErr := os.ReadFile(exePath)
	if readErr != nil {
		t.Fatalf("failed to read restored binary: %v", readErr)
	}
	if string(got) != "current-binary" {
		t.Errorf("backup was not restored, got %q", got)
	}
}

// ---------- ReleaseInfo struct tests ----------

func TestReleaseInfoStruct(t *testing.T) {
	info := ReleaseInfo{
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.2.0",
		UpdateAvail:    true,
		ReleaseURL:     "https://github.com/uwaserver/uwas/releases/tag/v1.2.0",
		PublishedAt:    "2024-01-01T00:00:00Z",
		ReleaseNotes:   "New features",
		DownloadURL:    "https://example.com/uwas-linux-amd64",
	}

	// Verify JSON serialization
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ReleaseInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q", decoded.CurrentVersion)
	}
	if decoded.LatestVersion != "1.2.0" {
		t.Errorf("LatestVersion = %q", decoded.LatestVersion)
	}
	if !decoded.UpdateAvail {
		t.Error("UpdateAvail should be true")
	}
	if decoded.ReleaseURL != info.ReleaseURL {
		t.Errorf("ReleaseURL = %q", decoded.ReleaseURL)
	}
	if decoded.PublishedAt != info.PublishedAt {
		t.Errorf("PublishedAt = %q", decoded.PublishedAt)
	}
	if decoded.ReleaseNotes != info.ReleaseNotes {
		t.Errorf("ReleaseNotes = %q", decoded.ReleaseNotes)
	}
	if decoded.DownloadURL != info.DownloadURL {
		t.Errorf("DownloadURL = %q", decoded.DownloadURL)
	}
}

func TestReleaseInfoStruct_OmitEmpty(t *testing.T) {
	info := ReleaseInfo{
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.0.0",
		UpdateAvail:    false,
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Fields with omitempty should be absent when empty
	s := string(data)
	if strings.Contains(s, "release_url") {
		t.Error("empty release_url should be omitted")
	}
	if strings.Contains(s, "published_at") {
		t.Error("empty published_at should be omitted")
	}
	if strings.Contains(s, "release_notes") {
		t.Error("empty release_notes should be omitted")
	}
	if strings.Contains(s, "download_url") {
		t.Error("empty download_url should be omitted")
	}
}

// ---------- Hook defaults test ----------

func TestHookDefaults(t *testing.T) {
	// Verify the default hooks are set to real implementations
	client := httpClientFn(5 * time.Second)
	if client == nil {
		t.Fatal("httpClientFn should return a non-nil client")
	}
	if client.Timeout != 5*time.Second {
		t.Errorf("client timeout = %v, want 5s", client.Timeout)
	}

	// runtimeGOOS and runtimeGOARCH should match runtime package
	if runtimeGOOS == "" {
		t.Error("runtimeGOOS should not be empty")
	}
	if runtimeGOARCH == "" {
		t.Error("runtimeGOARCH should not be empty")
	}
}

func TestCheckUpdate_RequestPath(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	var gotPath string
	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		rel := githubRelease{TagName: "v1.0.0", Assets: []githubAsset{}}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL

	_, err := CheckUpdate("v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := "/repos/uwaserver/uwas/releases/latest"
	if gotPath != wantPath {
		t.Errorf("request path = %q, want %q", gotPath, wantPath)
	}
}
