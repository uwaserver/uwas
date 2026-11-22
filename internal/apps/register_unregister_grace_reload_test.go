//go:build !windows

package apps

import (
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// TestRegisterUnregisterReleaseManagerLockDuringGrace: replacing or deleting a
// running app ran the SIGTERM grace window under m.mu, stalling every proxied
// request to every app until the old process exited (F68800).
func TestRegisterUnregisterReleaseManagerLockDuringGrace(t *testing.T) {
	origFree, origExec := isPortFreeFn, execCommandFn
	isPortFreeFn = func(int) bool { return true }
	execCommandFn = func(string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "trap '' TERM; exec sleep 1000")
	}
	defer func() { isPortFreeFn, execCommandFn = origFree, origExec }()

	m := exposedPortManager(t)
	reg := func(n, cmd string, port int) error {
		return m.Register(&App{Name: n, Runtime: RuntimeCustom, Command: cmd, Port: port, WorkDir: t.TempDir()})
	}
	for i, n := range []string{"unreg", "rereg", "other"} {
		if err := reg(n, "run", 45601+i); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"unreg", "rereg"} {
		if err := m.Start(n); err != nil {
			t.Fatal(err)
		}
	}
	pids := map[string]int{"unreg": m.Get("unreg").PID, "rereg": m.Get("rereg").PID}

	for _, tc := range []struct {
		name string
		op   func() error
	}{
		{"unreg", func() error { return m.Unregister("unreg") }},
		{"rereg", func() error { return reg("rereg", "run2", 45602) }},
	} {
		done := make(chan error, 1)
		go func() { done <- tc.op() }()
		stopLockWaitFor(t, func(s string) bool { return strings.Contains(s, "apps.gracefulKill") }, tc.name+" grace window")
		r := make(chan struct{})
		go func() { _ = m.ListenAddrForPort("other", 0); _ = m.Instances(); close(r) }()
		stopLockWaitFor(t, func(string) bool {
			select {
			case <-r:
				return true
			default:
				return false
			}
		}, "reader")
		if !strings.Contains(stopLockStacks(), "apps.gracefulKill") {
			t.Fatalf("%s: reader only returned after the grace window; m.mu was held through it", tc.name)
		}
		if err := m.Start(tc.name); err == nil || !strings.Contains(err.Error(), "already running") {
			t.Fatalf("%s: Start during the grace window: %v", tc.name, err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if syscall.Kill(-pids[tc.name], 0) == nil {
			t.Fatalf("%s: old process group still alive", tc.name)
		}
	}
	if m.Get("unreg") != nil || m.Store().Exists("unreg") {
		t.Fatal("unregistered app still present")
	}
	if inst := m.Get("rereg"); inst == nil || inst.Running || inst.Command != "run2" {
		t.Fatalf("re-registered app: %+v", inst)
	}
}

// TestStartAppliesReloadedDefinition: LoadAll re-pointed p.app at the edited
// file, but Start kept the command, env and port resolved at registration, so
// an apps.d edit never reached a Restart (F68801).
func TestStartAppliesReloadedDefinition(t *testing.T) {
	var mu sync.Mutex
	var last *exec.Cmd
	var lastArgs string
	origFree, origExec := isPortFreeFn, execCommandFn
	isPortFreeFn = func(int) bool { return true }
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		c := exec.Command("sh", "-c", "exec sleep 1000")
		mu.Lock()
		last, lastArgs = c, strings.Join(append([]string{name}, arg...), " ")
		mu.Unlock()
		return c
	}
	defer func() { isPortFreeFn, execCommandFn = origFree, origExec }()

	m := exposedPortManager(t)
	wd := t.TempDir()
	mk := func(cmd, val string, port int) *App {
		return &App{Name: "api", Runtime: RuntimeCustom, Command: cmd, Port: port, WorkDir: wd, Env: EnvFromPairs("APP_SECRET", val)}
	}
	if err := m.Register(mk("run-old", "v1", 45621)); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("api"); err != nil {
		t.Fatal(err)
	}
	if err := m.Store().Save(mk("run-new", "v2", 45622)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if got := m.ListenAddr("api"); got != "127.0.0.1:45621" {
		t.Fatalf("running app moved port before a restart: %s", got)
	}
	if err := m.Stop("api"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("api"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	args, env := lastArgs, strings.Join(last.Env, "\n")
	mu.Unlock()
	if !strings.Contains(args, "run-new") || !strings.Contains(env, "APP_SECRET=v2") || !strings.Contains(env, "PORT=45622") {
		t.Fatalf("restart after reload used the old definition: argv=%q", args)
	}
	if got := m.ListenAddr("api"); got != "127.0.0.1:45622" {
		t.Fatalf("listen address after reload+restart: %s", got)
	}
}
