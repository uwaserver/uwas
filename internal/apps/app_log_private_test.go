package apps

// F48301: an app's stdout/stderr log (and the docker build log) was created
// 0644 in a directory created 0755. App output carries whatever the app prints
// (tokens in stack traces, connection strings in startup banners, build output
// with registry credentials), and under a root UWAS the other local accounts
// include the tenant PHP workers, which can read the whole apps tree. The logs
// are now created 0600 in a 0750 directory; an existing log file from an older
// release is tightened too. An existing directory is left alone (with a
// custom work_dir it is a sibling directory the operator owns).

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func startSleepApp(t *testing.T, root string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	workDir := filepath.Join(root, "apps", "logapp")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, NewStore(filepath.Join(root, "apps.d")), nil)
	app := &App{Name: "logapp", Runtime: RuntimeCustom, Command: "sleep 5", WorkDir: workDir}
	if err := m.Register(app); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(app.Name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(app.Name) })
	return filepath.Join(root, "apps", "logs", "logapp.log")
}

func TestNativeAppLogIsPrivate(t *testing.T) {
	root := t.TempDir()
	logPath := startSleepApp(t, root)
	li, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(filepath.Dir(logPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: log 0600 (no group/other), dir without world access\nACTUAL: log %v dir %v", li.Mode().Perm(), di.Mode().Perm())
	if li.Mode().Perm()&0o077 != 0 {
		t.Errorf("PROBLEM CONFIRMED: app log mode %v is readable by other accounts", li.Mode().Perm())
	}
	if di.Mode().Perm()&0o007 != 0 {
		t.Errorf("PROBLEM CONFIRMED: new log directory mode %v is open to other accounts", di.Mode().Perm())
	}
}

// A log written by an older release is 0644; the next start tightens it. The
// directory it lives in is not UWAS's to chmod, so it keeps its mode.
func TestExistingAppLogIsTightenedDirKept(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "apps", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "logapp.log")
	if err := os.WriteFile(logPath, []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(logPath, 0o644); err != nil {
		t.Fatal(err)
	}
	startSleepApp(t, root)
	li, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(logDir)
	t.Logf("EXPECTED: legacy log tightened to 0600, dir untouched\nACTUAL: log %v dir %v", li.Mode().Perm(), di.Mode().Perm())
	if li.Mode().Perm()&0o077 != 0 {
		t.Errorf("PROBLEM CONFIRMED: legacy app log still %v", li.Mode().Perm())
	}
	if di.Mode().Perm() != 0o755 {
		t.Errorf("control: an existing log directory must keep its mode, got %v", di.Mode().Perm())
	}
	if b, _ := os.ReadFile(logPath); len(b) < len("old line\n") || string(b[:9]) != "old line\n" {
		t.Errorf("control: existing log content must be preserved, got %q", b)
	}
}

func TestDockerBuildLogIsPrivate(t *testing.T) {
	root := t.TempDir()
	ctxDir := filepath.Join(root, "ctx")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, NewStore(filepath.Join(root, "apps.d")), nil)
	workDir := filepath.Join(root, "apps", "blog")
	p := &process{name: "blog", workDir: workDir, app: &App{Name: "blog", Runtime: RuntimeDocker,
		WorkDir: workDir, Docker: DockerSpec{Image: "img:1", ContainerPort: 80, Build: DockerBuild{Context: ctxDir}}}}
	prev := execCommandFn
	execCommandFn = func(string, ...string) *exec.Cmd { return exec.Command("true") }
	t.Cleanup(func() { execCommandFn = prev })
	if err := m.buildImage(p, "img:1"); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "apps", "logs", "blog-build.log")
	li, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: build log 0600\nACTUAL: %v", li.Mode().Perm())
	if li.Mode().Perm()&0o077 != 0 {
		t.Errorf("PROBLEM CONFIRMED: docker build log mode %v is readable by other accounts", li.Mode().Perm())
	}
}
