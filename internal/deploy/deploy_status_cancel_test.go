package deploy

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStatusReturnsSnapshot pins that Status() hands out a copy: the deploy
// goroutine keeps mutating the live record, so an aliased pointer raced with
// readers and let a caller's write (e.g. Status="building") wedge the
// concurrent-deploy guard for that domain.
func TestStatusReturnsSnapshot(t *testing.T) {
	defer saveAndRestoreHooks()()
	gate := make(chan struct{})
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		if name == "git" && args[0] == "clone" {
			<-gate
		}
		if name == "git" && args[0] == "rev-parse" {
			return "abc1234\n", nil
		}
		return "", nil
	}
	runShellFn = func(dir string, env map[string]string, command string) (string, error) { return "", nil }
	waitForAppFn = func(string, time.Duration) error { return nil }

	m := New(nil)
	if m.Status("missing.com") != nil {
		t.Fatal("Status of unknown domain should be nil")
	}
	done := make(chan error, 1)
	m.Deploy(DeployRequest{Domain: "a.com", GitURL: "https://example.invalid/r.git", BuildCmd: "skip"}, t.TempDir()+"/app", func(err error) { done <- err })

	snap := m.Status("a.com")
	other := m.Status("a.com")
	// Read the snapshot concurrently with the deploy goroutine's writes; under
	// -race an aliased pointer is reported here.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = snap.Status + snap.CommitSHA + snap.Log
		}
	}()
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	wg.Wait()

	if snap.Status != "deploying" || snap.CommitSHA != "" {
		t.Fatalf("snapshot mutated after return: status=%q sha=%q", snap.Status, snap.CommitSHA)
	}
	snap.Status = "building"
	if other.Status != "deploying" {
		t.Fatalf("two Status() results share storage: %q", other.Status)
	}
	live := m.Status("a.com")
	if live.Status != "running" || live.CommitSHA != "abc1234" {
		t.Fatalf("live status = %q sha=%q, want running/abc1234", live.Status, live.CommitSHA)
	}
	// The caller write must not block a redeploy via the in-progress guard.
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) { return "", nil }
	done2 := make(chan error, 1)
	m.Deploy(DeployRequest{Domain: "a.com", GitURL: "https://example.invalid/r.git", BuildCmd: "skip"}, t.TempDir()+"/app", func(err error) { done2 <- err })
	if err := <-done2; err != nil && strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("redeploy rejected after caller mutated snapshot: %v", err)
	}
}

// dockerCancelHarness runs a docker-mode deploy whose runCmd blocks on the
// first command with the given prefix until the test releases it.
func dockerCancelHarness(t *testing.T, blockOn string) (m *Manager, reached, release chan struct{}, done chan error, ran func(prefix string) bool) {
	t.Helper()
	var mu sync.Mutex
	var cmds []string
	reached = make(chan struct{})
	release = make(chan struct{})
	var once sync.Once
	runCmdFn = func(dir string, env map[string]string, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		if blockOn != "" && strings.HasPrefix(cmd, blockOn) {
			once.Do(func() { close(reached); <-release })
		}
		return "cid\n", nil
	}
	waitForAppFn = func(string, time.Duration) error { return nil }
	ran = func(prefix string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range cmds {
			if strings.HasPrefix(c, prefix) {
				return true
			}
		}
		return false
	}
	m = New(nil)
	done = make(chan error, 1)
	m.Deploy(DeployRequest{Domain: "d.com", DockerFile: "Dockerfile", DockerPort: 3000}, t.TempDir(), func(err error) { done <- err })
	return
}

// TestCancelDeployStopsDockerDeploy pins that a cancel CancelDeploy accepted
// is honoured by docker mode at its step boundaries instead of being silently
// dropped while the deploy still reports "running".
func TestCancelDeployStopsDockerDeploy(t *testing.T) {
	t.Run("during build", func(t *testing.T) {
		defer saveAndRestoreHooks()()
		m, reached, release, done, ran := dockerCancelHarness(t, "docker build")
		<-reached
		if !m.CancelDeploy("d.com") {
			t.Fatal("CancelDeploy rejected an in-progress deploy")
		}
		close(release)
		err := <-done
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("err = %v, want deployment cancelled", err)
		}
		if st := m.Status("d.com"); st.Status != "failed" {
			t.Fatalf("status = %q, want failed", st.Status)
		}
		if ran("docker run") {
			t.Fatal("docker run executed after cancel")
		}
	})
	t.Run("before build", func(t *testing.T) {
		defer saveAndRestoreHooks()()
		m, reached, release, done, ran := dockerCancelHarness(t, "docker stop")
		<-reached
		if !m.CancelDeploy("d.com") {
			t.Fatal("CancelDeploy rejected an in-progress deploy")
		}
		close(release)
		if err := <-done; err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("err = %v, want deployment cancelled", err)
		}
		if ran("docker build") || ran("docker run") {
			t.Fatal("docker build/run executed after cancel")
		}
	})
	t.Run("no cancel", func(t *testing.T) {
		defer saveAndRestoreHooks()()
		m, _, _, done, ran := dockerCancelHarness(t, "")
		if err := <-done; err != nil {
			t.Fatalf("uncancelled deploy failed: %v", err)
		}
		if st := m.Status("d.com"); st.Status != "running" || !ran("docker run") {
			t.Fatalf("status = %q ranRun=%v, want running/true", st.Status, ran("docker run"))
		}
		if m.CancelDeploy("d.com") {
			t.Fatal("CancelDeploy after completion should report false")
		}
	})
}
