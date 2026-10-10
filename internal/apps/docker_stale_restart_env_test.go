package apps

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// staleDockerScript is a minimal fake docker CLI: containers are files under
// $FAKE_DOCKER_DIR/live/<name>, every argv is appended to $FAKE_DOCKER_DIR/log,
// `run` records the env each -e delivers (resolving bare `-e NAME` from its own
// environment like the real CLI), and `wait` blocks on a FIFO the test opens.
const staleDockerScript = `#!/bin/bash
D="$FAKE_DOCKER_DIR"; [ -n "$D" ] || exit 1
mkdir -p "$D/live"; printf '%s\n' "$*" >> "$D/log"
cmd="$1"; shift
case "$cmd" in
run)
  name=""; envs=()
  while [ $# -gt 0 ]; do case "$1" in
    --name) name="$2"; shift 2;;
    -e) if [[ "$2" == *=* ]]; then envs+=("$2"); else envs+=("$2=${!2}"); fi; shift 2;;
    -p|-v) shift 2;; *) shift;; esac; done
  [ -e "$D/live/$name" ] && { echo "name in use" >&2; exit 125; }
  n=$(( $(cat "$D/counter" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$D/counter"
  echo "c$n" > "$D/live/$name"; for e in "${envs[@]}"; do printf '%s\n' "$e" >> "$D/envs"; done
  echo "c$n";;
inspect) if [ "$1" = "-f" ]; then [ -e "$D/live/$3" ] && { echo true; exit 0; }; fi; exit 1;;
rm) rm -f "$D/live/${@: -1}";;
stop) for f in "$D"/live/*; do [ -e "$f" ] || continue
    { [ "$(basename "$f")" = "$1" ] || [ "$(cat "$f")" = "$1" ]; } && rm -f "$f"; done;;
wait) f="$D/wait_$1"; [ -p "$f" ] || mkfifo "$f" 2>/dev/null; read -r _ < "$f";;
esac
exit 0
`

type staleDocker struct {
	t   *testing.T
	dir string
}

func newStaleDocker(t *testing.T) *staleDocker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake docker is a bash script")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin, state := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(staleDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_DIR", state)
	prev := isPortFreeFn
	isPortFreeFn = func(int) bool { return true }
	t.Cleanup(func() { isPortFreeFn = prev })
	return &staleDocker{t: t, dir: state}
}

func (d *staleDocker) file(rel string) string {
	b, _ := os.ReadFile(filepath.Join(d.dir, rel))
	return string(b)
}

func (d *staleDocker) live(cname string) string {
	return strings.TrimSpace(d.file(filepath.Join("live", cname)))
}

// releaseWait lets the watcher's `docker wait <id>` return.
func (d *staleDocker) releaseWait(id string) {
	d.t.Helper()
	f := filepath.Join(d.dir, "wait_"+id)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(f); err == nil {
			break
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("watcher never waited on %s", id)
		}
		time.Sleep(time.Millisecond)
	}
	w, err := os.OpenFile(f, os.O_RDWR, 0) // O_RDWR never blocks on a FIFO
	if err != nil {
		d.t.Fatal(err)
	}
	_, _ = w.WriteString("x\n")
	_ = w.Close()
}

func dockerTestManager(t *testing.T, name string, env map[string]string) *Manager {
	t.Helper()
	m := NewManager(NewStore(t.TempDir()), nil)
	a := &App{Name: name, Runtime: RuntimeDocker, Port: 39998,
		Docker: DockerSpec{Image: "img:1", ContainerPort: 80}}
	if env != nil {
		a.Env = EnvFromMap(env)
	}
	if err := m.Register(a); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *Manager) testDockerState(name string) (id string, stopCh chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.procs[name].dockerID, m.procs[name].stopCh
}

// A docker (re)start superseded by a newer Start() — what a watcher whose
// auto-restart backoff was pending runs once the timer fires — must not touch
// the container that newer Start() launched. Its prior-cleanup `docker rm -f
// uwas-app-<name>` used to run before the stopCh identity check and
// force-remove the operator's fresh container.
func TestSupersededDockerRestartKeepsFreshContainer(t *testing.T) {
	d := newStaleDocker(t)
	m := dockerTestManager(t, "stale", nil)
	cname := containerName("stale")

	if err := m.Start("stale"); err != nil {
		t.Fatal(err)
	}
	c1, staleCh := m.testDockerState("stale")
	_ = os.Remove(filepath.Join(d.dir, "live", cname)) // container exits (--rm)
	d.releaseWait(c1)
	deadline := time.Now().Add(10 * time.Second)
	for id, _ := m.testDockerState("stale"); id != ""; id, _ = m.testDockerState("stale") {
		if time.Now().After(deadline) {
			t.Fatal("watcher never observed the exit")
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.Start("stale"); err != nil {
		t.Fatal(err)
	}
	fresh, _ := m.testDockerState("stale")
	t.Cleanup(func() {
		_ = m.Stop("stale") // close stopCh first so no auto-restart follows
		d.releaseWait(fresh)
	})

	logBefore := d.file("log")
	_ = m.startDocker(m.procs["stale"], staleCh) // the superseded continuation, gated after Start

	if got := d.live(cname); got != fresh || fresh == "" {
		t.Fatalf("live container = %q, want the fresh %q", got, fresh)
	}
	if extra := strings.TrimPrefix(d.file("log"), logBefore); extra != "" {
		t.Fatalf("superseded restart issued docker commands:\n%s", extra)
	}
}

// App env values must not appear on the docker CLI argv (world-readable via
// /proc/<pid>/cmdline for the whole `docker run`, image pull included); they
// are passed by name and resolved from the CLI's environment instead.
func TestDockerRunKeepsEnvValuesOffArgv(t *testing.T) {
	d := newStaleDocker(t)
	env := map[string]string{"API_TOKEN": "tok-value-1", "PEM": "line1\nline2", "EMPTY": ""}
	m := dockerTestManager(t, "envs", env)
	if err := m.Start("envs"); err != nil {
		t.Fatal(err)
	}
	id, _ := m.testDockerState("envs")
	t.Cleanup(func() {
		_ = m.Stop("envs")
		d.releaseWait(id)
	})

	var argv string
	for _, l := range strings.Split(d.file("log"), "\n") {
		if strings.HasPrefix(l, "run ") {
			argv = l
		}
	}
	delivered := d.file("envs")
	for k, v := range env {
		if v != "" && strings.Contains(argv, v) {
			t.Fatalf("value of %s on docker argv: %q", k, argv)
		}
		if !strings.Contains(delivered, k+"="+v) {
			t.Fatalf("%s not delivered to the container; got %q", k, delivered)
		}
	}
}
