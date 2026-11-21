package selfupdate

import (
	"encoding/json"
	"net/http"
	"testing"
)

func checkRelease(t *testing.T, goos, goarch, tag, current string, assets ...string) *ReleaseInfo {
	t.Helper()
	saveHooks(t)
	runtimeGOOS, runtimeGOARCH = goos, goarch
	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{TagName: tag}
		for _, a := range assets {
			rel.Assets = append(rel.Assets, githubAsset{Name: a, BrowserDownloadURL: "https://github.com/x/" + a})
		}
		json.NewEncoder(w).Encode(rel)
	})
	githubAPIBase = srv.URL
	info, err := CheckUpdate(current)
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	return info
}

// A substring match gave a linux/arm host the published linux-arm64 binary,
// which Update then installed over the running one (F67300).
func TestCheckUpdateAssetNameMustMatchExactly(t *testing.T) {
	published := []string{"SHA256SUMS", "uwas-linux-amd64.sha256", "uwas-linux-amd64", "uwas-linux-arm64", "uwas-windows-amd64.exe"}
	for _, c := range []struct{ goos, goarch, want string }{
		{"linux", "arm", ""},
		{"linux", "amd64", "https://github.com/x/uwas-linux-amd64"},
		{"linux", "arm64", "https://github.com/x/uwas-linux-arm64"},
		{"windows", "amd64", "https://github.com/x/uwas-windows-amd64.exe"},
	} {
		if got := checkRelease(t, c.goos, c.goarch, "v0.11.42", "v0.11.41", published...).DownloadURL; got != c.want {
			t.Errorf("%s/%s: download_url = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

// A host running a build newer than the latest release was offered an
// "update" that downgraded it (F67301).
func TestCheckUpdateDoesNotOfferDowngrade(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.11.41", "v0.12.0-rc1", false},
		{"v0.11.41", "v0.11.41-7-g06f08e20", false},
		{"v0.11.41", "v0.11.42", false},
		{"v0.12.0-rc1", "v0.12.0", false},
		{"v0.11.41", "v0.11.41", false},
		{"v0.11.41", "v0.11.40", true},
		{"v0.11.42", "v0.11.41-7-g06f08e20", true},
		{"v0.12.0", "v0.12.0-rc1", true},
		{"v0.12.0-rc.10", "v0.12.0-rc.2", true},
	} {
		if got := checkRelease(t, "linux", "amd64", c.latest, c.current).UpdateAvail; got != c.want {
			t.Errorf("latest %s, running %s: update_available = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
