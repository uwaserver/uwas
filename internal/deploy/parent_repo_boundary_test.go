package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeploymentDoesNotDiscoverParentRepository(t *testing.T) {
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	original := runCmdFn
	t.Cleanup(func() { runCmdFn = original })
	for _, mode := range []string{"valid", "empty-gitdir", "relative-empty-gitdir", "invalid-gitfile", "missing"} {
		parent := t.TempDir()
		git(parent, "init", "-q")
		git(parent, "remote", "add", "origin", "https://parent.test/original")
		child := filepath.Join(parent, "app")
		if err := os.Mkdir(child, 0755); err != nil {
			t.Fatal(err)
		}
		switch mode {
		case "valid":
			git(child, "init", "-q")
			git(child, "remote", "add", "origin", "https://child.test/original")
		case "empty-gitdir", "relative-empty-gitdir":
			if err := os.Mkdir(filepath.Join(child, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
		case "invalid-gitfile":
			if err := os.WriteFile(filepath.Join(child, ".git"), []byte("invalid"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		input := child
		if mode == "relative-empty-gitdir" {
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			input, err = filepath.Rel(cwd, child)
			if err != nil {
				t.Fatal(err)
			}
		}
		arrived, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
			if name == "git" && len(args) > 1 && args[0] == "remote" && args[1] == "set-url" {
				close(arrived)
				<-release
				return runCmdImpl(dir, env, name, args...)
			}
			return "", errors.New("injected stop before external operations")
		}
		go func() {
			var log strings.Builder
			done <- New(nil).deployGit(DeployRequest{GitURL: "https://child.test/repo", Env: map[string]string{"GIT_CEILING_DIRECTORIES": "/"}}, input, "main", nil, &DeployStatus{}, &log)
		}()
		if mode != "missing" {
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("remote gate not reached", mode)
			}
			if got := git(parent, "remote", "get-url", "origin"); got != "https://parent.test/original" {
				t.Fatal("parent changed before gate", got)
			}
		}
		close(release)
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("injected failure lost")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("deployment did not finish", mode)
		}
		if got := git(parent, "remote", "get-url", "origin"); got != "https://parent.test/original" {
			t.Fatalf("%s changed parent origin to %s", mode, got)
		}
		if mode == "valid" && git(child, "remote", "get-url", "origin") != "https://child.test/repo" {
			t.Fatal("valid child not updated")
		}
	}
}
