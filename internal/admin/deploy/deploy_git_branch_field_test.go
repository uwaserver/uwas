package deploy

// F64700: the manual deploy honours "git_branch" (dashboard/API
// field), keeps the legacy "branch", prefers git_branch when both are sent,
// falls back to the stored branch when neither is sent, and still rejects an
// invalid branch arriving through the new field.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

func deployWithBranchBody(t *testing.T, storedBranch string, body func(url string) string) (version, resp string) {
	t.Helper()
	serveURL, work := newServedRepo(t, "main-v1")
	git(t, work, "checkout", "-q", "-b", "develop")
	if err := os.WriteFile(filepath.Join(work, "version.txt"), []byte("develop-v1"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "develop")
	git(t, work, "push", "-q", "origin", "develop")

	store := apps.NewStore(t.TempDir())
	store.DataRoot = t.TempDir()
	if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, Command: "true",
		Deploy: apps.DeployConfig{GitURL: serveURL, GitBranch: storedBranch}}); err != nil {
		t.Fatal(err)
	}
	h := New(&branchFilterDeps{mgr: apps.NewManager(store, nil)})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/apps/demo/deploy", strings.NewReader(body(serveURL)))
	r.SetPathValue("name", "demo")
	rec := httptest.NewRecorder()
	h.Deploy(rec, r)
	resp = rec.Body.String()
	if !strings.Contains(resp, `"ok":true`) {
		return "", resp
	}
	def, err := store.Get("demo")
	if err != nil || def == nil || def.WorkDir == "" {
		t.Fatalf("no work dir: %v %#v", err, def)
	}
	return readVersion(t, def.WorkDir), resp
}

func TestDeployHonoursGitBranchField(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	failed := false
	check := func(label, stored, want string, body func(string) string) {
		got, resp := deployWithBranchBody(t, stored, body)
		ok := got == want
		if want == "" { // expected rejection
			ok = got == "" && strings.Contains(resp, "invalid git branch")
		}
		t.Logf("%-34s expected %-12q actual %-12q ok=%v", label, want, got, ok)
		if !ok {
			failed = true
			t.Logf("response: %s", resp)
		}
	}
	check("git_branch=develop (repro)", "", "develop-v1", func(u string) string {
		return fmt.Sprintf(`{"git_url":%q,"git_branch":"develop"}`, u)
	})
	check("legacy branch=develop", "", "develop-v1", func(u string) string {
		return fmt.Sprintf(`{"git_url":%q,"branch":"develop"}`, u)
	})
	check("both: git_branch wins", "", "develop-v1", func(u string) string {
		return fmt.Sprintf(`{"git_url":%q,"git_branch":"develop","branch":"main"}`, u)
	})
	check("neither: stored develop", "develop", "develop-v1", func(u string) string {
		return `{}`
	})
	check("empty git_branch: stored main", "main", "main-v1", func(u string) string {
		return fmt.Sprintf(`{"git_url":%q,"git_branch":""}`, u)
	})
	check("invalid git_branch rejected", "", "", func(u string) string {
		return fmt.Sprintf(`{"git_url":%q,"git_branch":"main;id"}`, u)
	})
	if failed {
		t.Error("deploy branch selection regressed")
	}
}
