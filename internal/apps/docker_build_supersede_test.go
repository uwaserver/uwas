package apps

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// buildGateScript is a minimal fake docker CLI: containers are files under
// $FAKE_DOCKER_DIR/live/<name>, every argv is appended to $FAKE_DOCKER_DIR/log,
// and `wait` blocks on a FIFO the test opens. `buildx` parks on the "gate" FIFO
// while $D/armed exists (first build only), so the order in which two Start()
// calls finish is gated by the test, never by timing.
const buildGateScript = `#!/bin/bash
D="$FAKE_DOCKER_DIR"; [ -n "$D" ] || exit 1
mkdir -p "$D/live"; printf '%s\n' "$*" >> "$D/log"
cmd="$1"; shift
case "$cmd" in
buildx)
  if [ -e "$D/armed" ]; then rm -f "$D/armed"; touch "$D/building"; read -r _ < "$D/gate"; fi;;
run)
  name=""
  while [ $# -gt 0 ]; do case "$1" in
    --name) name="$2"; shift 2;;
    -p|-v|-e) shift 2;; *) shift;; esac; done
  [ -e "$D/live/$name" ] && { echo "name in use" >&2; exit 125; }
  n=$(( $(cat "$D/counter" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$D/counter"
  echo "c$n" > "$D/live/$name"
  echo "c$n";;
inspect) if [ "$1" = "-f" ]; then [ -e "$D/live/$3" ] && { echo true; exit 0; }; fi; exit 1;;
rm) rm -f "$D/live/${@: -1}";;
stop) for f in "$D"/live/*; do [ -e "$f" ] || continue
    { [ "$(basename "$f")" = "$1" ] || [ "$(cat "$f")" = "$1" ]; } && rm -f "$f"; done;;
wait) f="$D/wait_$1"; [ -p "$f" ] || mkfifo "$f" 2>/dev/null; read -r _ < "$f";;
esac
exit 0
`

type buildGateDocker struct {
	t   *testing.T
	dir string
	m   *Manager
	ctx string
}

func newBuildGateDocker(t *testing.T) *buildGateDocker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake docker is a bash script")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	root := t.TempDir()
	bin, state, ctx := filepath.Join(root, "bin"), filepath.Join(root, "state"), filepath.Join(root, "ctx")
	for _, d := range []string{bin, state, ctx} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(buildGateScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(state, "gate"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_DIR", state)
	prev := isPortFreeFn
	isPortFreeFn = func(int) bool { return true }
	t.Cleanup(func() { isPortFreeFn = prev })
	return &buildGateDocker{t: t, dir: state, ctx: ctx,
		m: testManager(t, NewStore(filepath.Join(root, "apps.d")), nil)}
}

func (d *buildGateDocker) register(name string, port int) {
	d.t.Helper()
	a := &App{Name: name, Runtime: RuntimeDocker, Port: port, WorkDir: filepath.Join(d.dir, "work", name),
		Docker: DockerSpec{Image: "img:1", ContainerPort: 80, Build: DockerBuild{Context: d.ctx}}}
	if err := d.m.Register(a); err != nil {
		d.t.Fatal(err)
	}
}

func (d *buildGateDocker) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(d.dir, rel))
	return strings.TrimSpace(string(b))
}

func (d *buildGateDocker) until(what string, cond func() bool) {
	d.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			d.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (d *buildGateDocker) dockerID(name string) string {
	d.m.mu.Lock()
	defer d.m.mu.Unlock()
	return d.m.procs[name].dockerID
}

// releaseWait lets the watcher's `docker wait <id>` return.
func (d *buildGateDocker) releaseWait(id string) {
	d.t.Helper()
	f := filepath.Join(d.dir, "wait_"+id)
	d.until("watcher wait on "+id, func() bool { _, err := os.Stat(f); return err == nil })
	w, err := os.OpenFile(f, os.O_RDWR, 0) // O_RDWR never blocks on a FIFO
	if err != nil {
		d.t.Fatal(err)
	}
	_, _ = w.WriteString("x\n")
	_ = w.Close()
}

// startParkedInBuild makes the next `docker buildx build` park until releaseBuild, and
// returns once a Start() is parked inside it.
func (d *buildGateDocker) startParkedInBuild(name string) <-chan error {
	d.t.Helper()
	if err := os.WriteFile(filepath.Join(d.dir, "armed"), nil, 0o644); err != nil {
		d.t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { errc <- d.m.Start(name) }()
	d.until("the build to park", func() bool {
		_, err := os.Stat(filepath.Join(d.dir, "building"))
		return err == nil
	})
	return errc
}

// releaseBuild lets the parked build finish and waits for its Start() to return.
func (d *buildGateDocker) releaseBuild(errc <-chan error) error {
	d.t.Helper()
	gate, err := os.OpenFile(filepath.Join(d.dir, "gate"), os.O_RDWR, 0)
	if err != nil {
		d.t.Fatal(err)
	}
	defer gate.Close()
	_, _ = gate.WriteString("x\n")
	return <-errc
}

// A docker Start() superseded by a newer Start() while its image build is still
// running must not touch the container the newer Start() launched: the stale
// run used to reach `docker rm -f uwas-app-<name>` after the build, because the
// supersession check only ran before it.
func TestDockerBuildWindowSupersededStartKeepsFreshContainer(t *testing.T) {
	d := newBuildGateDocker(t)
	d.register("bs", 39997)
	cname := containerName("bs")

	stale := d.startParkedInBuild("bs")
	if err := d.m.Start("bs"); err != nil { // supersedes the parked run
		t.Fatal(err)
	}
	fresh := d.dockerID("bs")
	t.Cleanup(func() {
		_ = d.m.Stop("bs")
		d.releaseWait(fresh)
	})
	if got := d.read("live/" + cname); got != fresh || fresh == "" {
		t.Fatalf("precondition: live container = %q, want the fresh %q", got, fresh)
	}
	logBefore := d.read("log")

	if err := d.releaseBuild(stale); err != nil {
		t.Fatalf("superseded Start returned %v", err)
	}
	if got := d.read("live/" + cname); got != fresh {
		t.Fatalf("live container = %q, want the fresh %q (the superseded run removed it)", got, fresh)
	}
	if got := d.dockerID("bs"); got != fresh {
		t.Fatalf("manager tracks %q, want %q", got, fresh)
	}
	extra := strings.TrimPrefix(d.read("log"), logBefore)
	for _, l := range strings.Split(extra, "\n") {
		if strings.HasPrefix(l, "rm ") || strings.HasPrefix(l, "run ") || strings.HasPrefix(l, "stop ") {
			t.Fatalf("superseded Start issued %q after its build", l)
		}
	}
}

// Stop() landing while the build runs must also keep the run from creating a
// container afterwards.
func TestDockerStopDuringBuildStartsNoContainer(t *testing.T) {
	d := newBuildGateDocker(t)
	d.register("bs", 39997)

	parked := d.startParkedInBuild("bs")
	if err := d.m.Stop("bs"); err != nil {
		t.Fatal(err)
	}
	if err := d.releaseBuild(parked); err != nil {
		t.Fatalf("stopped Start returned %v", err)
	}
	if got := d.read("live/" + containerName("bs")); got != "" {
		t.Fatalf("container %q exists after Stop during the build", got)
	}
	for _, l := range strings.Split(d.read("log"), "\n") {
		if strings.HasPrefix(l, "run ") || strings.HasPrefix(l, "rm ") {
			t.Fatalf("stopped Start issued %q", l)
		}
	}
}

// Control: a build nothing supersedes still ends with its container running,
// and a second Start while it runs is refused.
func TestDockerBuildThenRunUnsupersededKeepsContainer(t *testing.T) {
	d := newBuildGateDocker(t)
	d.register("bs", 39997)

	parked := d.startParkedInBuild("bs")
	if err := d.releaseBuild(parked); err != nil {
		t.Fatal(err)
	}
	id := d.dockerID("bs")
	t.Cleanup(func() {
		_ = d.m.Stop("bs")
		d.releaseWait(id)
	})
	if got := d.read("live/" + containerName("bs")); got != id || id == "" {
		t.Fatalf("live container = %q, want %q", got, id)
	}
	if err := d.m.Start("bs"); err == nil {
		t.Fatal("second Start of a running app succeeded")
	}
}
