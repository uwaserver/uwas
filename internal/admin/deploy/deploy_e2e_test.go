package deploy

import (
	"context"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

// gitServer publishes repositories over real HTTPS using git's own
// git-http-backend CGI.
//
// Serving them properly rather than pointing the deploy at a directory is
// deliberate: the pipeline rejects file:// URLs and pins GIT_ALLOW_PROTOCOL to
// https:ssh:git, and both of those are security controls worth more than the
// convenience of a shorter test. This exercises the shipped code path exactly
// as a real GitHub clone would.
func gitServer(t *testing.T, root string) string {
	t.Helper()

	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git --exec-path unavailable: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend not found: %v", err)
	}

	srv := httptest.NewTLSServer(&cgi.Handler{
		Path: backend,
		Env: []string{
			"GIT_PROJECT_ROOT=" + root,
			"GIT_HTTP_EXPORT_ALL=1",
		},
	})
	t.Cleanup(srv.Close)

	// The test server uses a self-signed certificate. GIT_SSL_NO_VERIFY reaches
	// git because the deploy builds its environment from os.Environ(); only
	// GIT_ALLOW_PROTOCOL is overridden, and https is already in its list.
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	return srv.URL
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newServedRepo creates a working repo plus the bare repo the HTTPS server
// publishes, mirroring the real shape: you commit and push, the server holds
// the result, the deploy pulls from the server.
func newServedRepo(t *testing.T, content string) (serveURL, workRepo string) {
	t.Helper()
	root := t.TempDir()

	bare := filepath.Join(root, "repo.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", bare)

	base := gitServer(t, root)

	workRepo = filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(workRepo, 0755); err != nil {
		t.Fatal(err)
	}
	git(t, workRepo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(workRepo, "version.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, workRepo, "add", "-A")
	git(t, workRepo, "commit", "-q", "-m", "initial")
	git(t, workRepo, "remote", "add", "origin", bare)
	git(t, workRepo, "push", "-q", "origin", "main")

	return base + "/repo.git", workRepo
}

// commitAndPush is the operator's half of the loop: edit, commit, push.
func commitAndPush(t *testing.T, workRepo, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workRepo, "version.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, workRepo, "add", "-A")
	git(t, workRepo, "commit", "-q", "-m", "update")
	git(t, workRepo, "push", "-q", "origin", "main")
}

func readVersion(t *testing.T, work string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(work, "version.txt"))
	if err != nil {
		t.Fatalf("read version.txt: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// TestDeployPicksUpPushedCommits is the whole question in one test: commit,
// push, deploy, and does the new version actually land? A pipeline that clones
// once and then never updates would pass a naive first-deploy smoke test, so
// the second half is the part that matters.
func TestDeployPicksUpPushedCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	url, src := newServedRepo(t, "v1")
	work := filepath.Join(t.TempDir(), "app")
	def := &apps.App{Name: "testapp", WorkDir: work}

	var log strings.Builder
	if err := runDeployCore(context.Background(), def, url, "main", "", "", "", nil, &log); err != nil {
		t.Fatalf("first deploy: %v\n%s", err, log.String())
	}
	if got := readVersion(t, work); got != "v1" {
		t.Fatalf("after first deploy version = %q, want v1", got)
	}

	commitAndPush(t, src, "v2")

	log.Reset()
	if err := runDeployCore(context.Background(), def, url, "main", "", "", "", nil, &log); err != nil {
		t.Fatalf("second deploy: %v\n%s", err, log.String())
	}
	if got := readVersion(t, work); got != "v2" {
		t.Fatalf("after pushing v2 and redeploying, version = %q — the pipeline is not picking up new commits\n%s",
			got, log.String())
	}

	// A third round, because "updates once" and "keeps updating" are not the
	// same claim.
	commitAndPush(t, src, "v3")
	log.Reset()
	if err := runDeployCore(context.Background(), def, url, "main", "", "", "", nil, &log); err != nil {
		t.Fatalf("third deploy: %v\n%s", err, log.String())
	}
	if got := readVersion(t, work); got != "v3" {
		t.Errorf("after a third push version = %q, want v3", got)
	}
}

// TestDeployRunsBuildCommand checks the build step runs in the work directory,
// since fetching without building ships stale artifacts.
func TestDeployRunsBuildCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	url, _ := newServedRepo(t, "v1")
	work := filepath.Join(t.TempDir(), "app")
	def := &apps.App{Name: "testapp", WorkDir: work}

	var log strings.Builder
	if err := runDeployCore(context.Background(), def, url, "main", "touch built.txt", "", "", nil, &log); err != nil {
		t.Fatalf("deploy: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(filepath.Join(work, "built.txt")); err != nil {
		t.Errorf("build command did not run in the work dir: %v\nlog:\n%s", err, log.String())
	}
}

// TestDeployFailsOnBadBuild pins that a failing build is reported rather than
// passing silently — that failure is what triggers the rollback path.
func TestDeployFailsOnBadBuild(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	url, _ := newServedRepo(t, "v1")
	work := filepath.Join(t.TempDir(), "app")
	def := &apps.App{Name: "testapp", WorkDir: work}

	var log strings.Builder
	if err := runDeployCore(context.Background(), def, url, "main", "false", "", "", nil, &log); err == nil {
		t.Error("a build exiting non-zero reported success")
	}
}
