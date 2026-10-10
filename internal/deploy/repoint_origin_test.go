package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// F1001: `git fetch origin` uses the URL stored in .git/config. Re-deploying
// an existing checkout after the app's git_url changed must repoint origin
// whether or not a token is configured, else the old repository is deployed.
func TestDeployGitExistingRepoRepointsOrigin(t *testing.T) {
	defer saveAndRestoreHooks()()
	run := func(token string) (setURL string, fetchedAfter bool) {
		appRoot := t.TempDir()
		os.MkdirAll(filepath.Join(appRoot, ".git"), 0o755)
		runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
			cmd := strings.Join(args, " ")
			if strings.HasPrefix(cmd, "remote set-url") {
				setURL = cmd
			}
			if strings.HasPrefix(cmd, "fetch") {
				fetchedAfter = setURL != ""
			}
			return "", nil
		}
		m := New(nil)
		st := &DeployStatus{Domain: "x.com", Status: "deploying", StartedAt: time.Now()}
		var log strings.Builder
		req := DeployRequest{Domain: "x.com", GitURL: "https://github.com/new/repo.git", GitBranch: "main", GitToken: token, BuildCmd: "none"}
		if err := m.deployGit(req, appRoot, "main", nil, st, &log); err != nil {
			t.Fatal(err)
		}
		return
	}
	// control: with a token the origin is repointed before fetch
	if s, f := run("tok"); s != "remote set-url origin https://github.com/new/repo.git" || !f {
		t.Fatalf("control failed: %q %v", s, f)
	}
	s, f := run("")
	if s != "remote set-url origin https://github.com/new/repo.git" || !f {
		t.Fatalf("EXPECTED: set-url origin <new url> before fetch\nACTUAL: set-url=%q beforeFetch=%v\nPROBLEM CONFIRMED", s, f)
	}
	t.Log("PROBLEM NOT REPRODUCED")
}
