package cronjob

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedCrontab fakes crontab: `crontab -l` returns state, `crontab <file>`
// replaces it. The first `crontab -l` call is held until release is closed.
type gatedCrontab struct {
	mu      sync.Mutex
	state   string
	reads   int
	held    chan struct{}
	release chan struct{}
}

func (g *gatedCrontab) command(t *testing.T) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		if len(args) == 1 && args[0] == "-l" {
			g.mu.Lock()
			g.reads++
			first := g.reads == 1
			g.mu.Unlock()
			if first {
				close(g.held)
				<-g.release
			}
			g.mu.Lock()
			cur := g.state
			g.mu.Unlock()
			return fakeExecCommand(cur, 0)(name, args...)
		}
		b, err := os.ReadFile(args[0])
		if err != nil {
			t.Errorf("read temp crontab: %v", err)
		}
		g.mu.Lock()
		g.state = string(b)
		g.mu.Unlock()
		return fakeExecCommand("", 0)(name, args...)
	}
}

// waitForCrontabLockWaiter returns once a goroutine is parked in lockCrontab.
// The deadline only bounds a failure; it does not order anything.
func waitForCrontabLockWaiter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		buf := make([]byte, 1<<20)
		for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(g, "sync.(*Mutex).Lock") && strings.Contains(g, "cronjob.lockCrontab") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("second crontab writer never waited for the first: read-modify-write is not serialized")
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

// Two concurrent crontab edits used to read the same content, and the second
// write dropped the first one's change (for example a domain delete's
// RemoveByDomain racing a CronAdd).
func TestCrontabEditsAreSerialized(t *testing.T) {
	cases := []struct {
		name  string
		first func() error
		check func(string) bool
	}{
		{
			name:  "Add vs Add",
			first: func() error { return Add(Job{Schedule: "*/5 * * * *", Command: "/bin/job-a", Domain: "a.com"}) },
			check: func(s string) bool { return strings.Contains(s, "/bin/job-a") },
		},
		{
			name:  "RemoveByDomain vs Add",
			first: func() error { return RemoveByDomain("old.com") },
			check: func(s string) bool { return !strings.Contains(s, "/bin/old") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origGOOS, origCmd := runtimeGOOS, execCommandFn
			defer func() { runtimeGOOS, execCommandFn = origGOOS, origCmd }()
			runtimeGOOS = "linux"
			g := &gatedCrontab{
				state:   "0 * * * * /usr/bin/other\n# UWAS managed [old.com] \n0 1 * * * /bin/old\n",
				held:    make(chan struct{}),
				release: make(chan struct{}),
			}
			execCommandFn = g.command(t)

			errs := make(chan error, 2)
			go func() { errs <- tc.first() }()
			<-g.held
			go func() { errs <- Add(Job{Schedule: "*/5 * * * *", Command: "/bin/job-b", Domain: "b.com"}) }()
			waitForCrontabLockWaiter(t)
			close(g.release)
			for i := 0; i < 2; i++ {
				if err := <-errs; err != nil {
					t.Fatalf("crontab edit failed: %v", err)
				}
			}

			g.mu.Lock()
			final := g.state
			g.mu.Unlock()
			if !tc.check(final) || !strings.Contains(final, "/bin/job-b") || !strings.Contains(final, "/usr/bin/other") {
				t.Fatalf("an edit was lost:\n%s", final)
			}
		})
	}
}

// A Schedule is not checked by ValidateShellCommand, and cron runs whatever
// follows the fifth field, so Add must reject anything but 5 fields or an
// @shorthand.
func TestAddRejectsScheduleCarryingCommand(t *testing.T) {
	origGOOS, origCmd := runtimeGOOS, execCommandFn
	defer func() { runtimeGOOS, execCommandFn = origGOOS, origCmd }()
	runtimeGOOS = "linux"
	g := &gatedCrontab{held: make(chan struct{}), release: make(chan struct{})}
	close(g.release)
	execCommandFn = g.command(t)

	for _, s := range []string{"* * * * * touch /tmp/x;", "* * * * $(id)", "* * * *", "@daily extra"} {
		if err := Add(Job{Schedule: s, Command: "true"}); err == nil {
			t.Errorf("Add accepted schedule %q", s)
		}
	}
	if g.state != "" {
		t.Fatalf("crontab written for a rejected schedule:\n%s", g.state)
	}
	for _, s := range []string{"0 9 * * mon-fri", "@daily"} {
		if err := Add(Job{Schedule: s, Command: "/bin/ok-" + strings.ReplaceAll(strings.ReplaceAll(s, " ", "_"), "*", "x")}); err != nil {
			t.Errorf("Add rejected valid schedule %q: %v", s, err)
		}
	}
}
