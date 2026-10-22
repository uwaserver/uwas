package deploy

// F23802: the container is named "uwas-" + sanitizeName(domain), and
// sanitizeName maps '.', '-' (and every other character) to '-'. "a.b.com" and
// "a-b.com" are different sites with the same container name, so deploying one
// ran `docker stop` + `docker rm` on the other site's live container. A
// container that carries another domain's owner label must be left alone;
// containers without a label (created before the label existed) and absent
// containers deploy as before.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func dockerCollisionRun(t *testing.T, domain string, inspect func() (string, error)) (cmds []string, err error) {
	t.Helper()
	defer saveAndRestoreHooks()()
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		c := name + " " + strings.Join(args, " ")
		cmds = append(cmds, c)
		if strings.HasPrefix(c, "docker inspect") {
			return inspect()
		}
		return "cid\n", nil
	}
	waitForAppFn = func(string, time.Duration) error { return nil }
	m := New(nil)
	done := make(chan error, 1)
	m.Deploy(DeployRequest{Domain: domain, DockerFile: "Dockerfile", DockerPort: 3000}, t.TempDir(), func(e error) { done <- e })
	return cmds, <-done
}

func touchesContainer(cmds []string) bool {
	for _, c := range cmds {
		if strings.HasPrefix(c, "docker stop") || strings.HasPrefix(c, "docker rm") {
			return true
		}
	}
	return false
}

func TestDeployDockerDoesNotReplaceAnotherDomainsContainer(t *testing.T) {
	cmds, err := dockerCollisionRun(t, "a-b.com", func() (string, error) { return "a.b.com\n", nil })
	t.Logf("EXPECTED: no docker stop/rm of a container owned by a.b.com, deploy refused\nACTUAL: err=%v cmds=%v", err, cmds)
	if touchesContainer(cmds) {
		t.Fatal("PROBLEM CONFIRMED: a-b.com stopped and removed the container owned by a.b.com")
	}
	if err == nil {
		t.Fatal("deploy must be refused on a container-name collision")
	}
}

func TestDeployDockerReplacesOwnOrLegacyOrMissingContainer(t *testing.T) {
	cases := []struct {
		name    string
		inspect func() (string, error)
	}{
		{"own label", func() (string, error) { return "a-b.com\n", nil }},
		{"legacy container without label", func() (string, error) { return "\n", nil }},
		{"no container yet", func() (string, error) { return "", errors.New("exit status 1") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmds, err := dockerCollisionRun(t, "a-b.com", c.inspect)
			if err != nil {
				t.Fatalf("deploy failed: %v (%v)", err, cmds)
			}
			if !touchesContainer(cmds) {
				t.Fatalf("expected the old container to be replaced: %v", cmds)
			}
		})
	}
}

func TestDeployDockerLabelsContainerWithOwner(t *testing.T) {
	cmds, err := dockerCollisionRun(t, "a-b.com", func() (string, error) { return "", errors.New("exit status 1") })
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		if strings.HasPrefix(c, "docker run") {
			if !strings.Contains(c, "--label uwas.domain=a-b.com") {
				t.Fatalf("docker run lacks the owner label: %s", c)
			}
			return
		}
	}
	t.Fatalf("no docker run: %v", cmds)
}
