package apps

// F48300: `docker buildx build --build-arg NAME=VALUE` put every build-arg
// value on the docker CLI's argv. A build runs for minutes and
// /proc/<pid>/cmdline is world-readable, so a registry or package token passed
// as a build arg was visible to every local account (including tenant PHP) for
// the whole build. `docker run` already keeps env values off argv (F646); the
// build now does the same: each arg is passed by name and its value goes in the
// CLI's own environment.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureBuildCmd runs buildImage with execCommandFn replaced by a recorder
// whose command succeeds immediately, and returns the argv and environment the
// docker CLI would have been started with.
func captureBuildCmd(t *testing.T, args map[string]string) (argv []string, env []string) {
	t.Helper()
	root := t.TempDir()
	ctxDir := filepath.Join(root, "ctx")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, NewStore(filepath.Join(root, "apps.d")), nil)
	workDir := filepath.Join(root, "work", "bargs")
	p := &process{
		name:    "bargs",
		workDir: workDir,
		app: &App{Name: "bargs", Runtime: RuntimeDocker, WorkDir: workDir,
			Docker: DockerSpec{Image: "img:1", ContainerPort: 80,
				Build: DockerBuild{Context: ctxDir, Args: args, Target: "prod"}}},
	}
	var last *exec.Cmd
	prev := execCommandFn
	execCommandFn = func(name string, a ...string) *exec.Cmd {
		argv = append([]string{name}, a...)
		last = exec.Command("true")
		return last
	}
	t.Cleanup(func() { execCommandFn = prev })
	if err := m.buildImage(p, "img:1"); err != nil {
		t.Fatalf("buildImage: %v", err)
	}
	if last == nil {
		t.Fatal("buildImage did not start a command")
	}
	return argv, last.Env
}

func hasFlagPair(argv []string, flag, val string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == val {
			return true
		}
	}
	return false
}

func TestDockerBuildArgValuesStayOffArgv(t *testing.T) {
	argv, env := captureBuildCmd(t, map[string]string{"NPM_TOKEN": "s3cret-npm-token", "APP_MODE": "prod"})
	joined := strings.Join(argv, " ")
	t.Logf("EXPECTED: no build-arg value on argv, values in the CLI env\nACTUAL: %s", joined)

	if strings.Contains(joined, "s3cret-npm-token") || strings.Contains(joined, "APP_MODE=prod") {
		t.Fatalf("PROBLEM CONFIRMED: build-arg value is on the docker argv: %s", joined)
	}
	if !hasFlagPair(argv, "--build-arg", "NPM_TOKEN") || !hasFlagPair(argv, "--build-arg", "APP_MODE") {
		t.Fatalf("build args must be passed by name (--build-arg KEY): %s", joined)
	}
	want := map[string]bool{"NPM_TOKEN=s3cret-npm-token": false, "APP_MODE=prod": false}
	for _, e := range env {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("value %q missing from the docker CLI environment", k)
		}
	}
	// Control: the rest of the command line is untouched.
	if !hasFlagPair(argv, "--target", "prod") || !hasFlagPair(argv, "--tag", "img:1") {
		t.Errorf("--tag/--target changed: %s", joined)
	}
}

// Keys the docker CLI or Go runtime would react to keep the inline form, so a
// build arg cannot redirect or reconfigure the CLI (same rule as `-e`).
func TestDockerBuildArgCLIKeysStayInline(t *testing.T) {
	argv, env := captureBuildCmd(t, map[string]string{"HTTP_PROXY": "http://proxy:3128", "DOCKER_HOST": "tcp://x:1"})
	if !hasFlagPair(argv, "--build-arg", "HTTP_PROXY=http://proxy:3128") ||
		!hasFlagPair(argv, "--build-arg", "DOCKER_HOST=tcp://x:1") {
		t.Fatalf("CLI-reactive keys must keep the inline form: %v", argv)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "HTTP_PROXY=http://proxy:3128") || strings.HasPrefix(e, "DOCKER_HOST=tcp://x:1") {
			t.Errorf("CLI-reactive key leaked into the CLI environment: %q", e)
		}
	}
}

func TestDockerBuildNoArgsNoEnvChange(t *testing.T) {
	argv, env := captureBuildCmd(t, nil)
	for _, a := range argv {
		if a == "--build-arg" {
			t.Fatalf("unexpected --build-arg: %v", argv)
		}
	}
	if env != nil {
		t.Errorf("a build without args must inherit the environment untouched, got %d entries", len(env))
	}
}
