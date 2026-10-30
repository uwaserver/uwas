package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// F43300: UWAS reaches a native app only through 127.0.0.1 (the reverse proxy,
// WaitListening and the Docker path, which publishes 127.0.0.1:port). It still
// told every app HOST=0.0.0.0 and its detected start commands and demo apps
// bound every interface, so an app's port was reachable from the network
// around UWAS's WAF, rate limiting, bot guard, TLS and auth. The defaults now
// bind loopback; an operator who needs another interface sets HOST or the
// command explicitly.

func TestNativeAppHostDefaultsToLoopback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dump := func(t *testing.T, name string, env map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		script := "#!/bin/sh\nenv > env.out\nsleep 2\n"
		if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		m := testManager(t, NewStore(t.TempDir()), nil)
		app := &App{Name: name, Runtime: RuntimeCustom, Command: "sh run.sh", WorkDir: dir}
		for k, v := range env {
			app.Env = EnvFromPairs(k, v)
		}
		if err := m.Register(app); err != nil {
			t.Fatal(err)
		}
		if err := m.Start(app.Name); err != nil {
			t.Fatal(err)
		}
		defer m.Stop(app.Name)
		for i := 0; i < 100; i++ {
			if b, err := os.ReadFile(filepath.Join(dir, "env.out")); err == nil && len(b) > 0 {
				return string(b)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("app wrote no environment dump")
		return ""
	}

	out := dump(t, "loopback-default", nil)
	t.Logf("EXPECTED: HOST=127.0.0.1\nACTUAL: %s", lineWith(out, "HOST="))
	if !strings.Contains(out, "\nHOST=127.0.0.1\n") && !strings.HasPrefix(out, "HOST=127.0.0.1\n") {
		t.Errorf("PROBLEM CONFIRMED: app HOST = %q, want 127.0.0.1", lineWith(out, "HOST="))
	}
	// Control: the port variables the proxy relies on are still set.
	for _, want := range []string{"PORT=", "UWAS_ASSIGNED_PORT="} {
		if !strings.Contains(out, want) {
			t.Errorf("app environment lost %q", want)
		}
	}

	// Control: an operator's explicit HOST still wins.
	out = dump(t, "loopback-override", map[string]string{"HOST": "0.0.0.0"})
	if !strings.Contains(out, "HOST=0.0.0.0") {
		t.Errorf("explicit HOST=0.0.0.0 was not honoured: %q", lineWith(out, "HOST="))
	}
}

func lineWith(s, prefix string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func TestDetectedStartCommandsBindLoopback(t *testing.T) {
	write := func(t *testing.T, files ...string) string {
		dir := t.TempDir()
		for _, f := range files {
			content := "#"
			if f == "package.json" {
				content = `{"scripts":{"preview":"vite preview"}}`
			}
			if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	cases := []struct {
		name, runtime string
		files         []string
	}{
		{"django", "python", []string{"manage.py"}},
		{"gunicorn", "python", []string{"requirements.txt"}},
		{"puma", "ruby", []string{"config.ru"}},
		{"vite preview", "node", []string{"package.json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectCommand(c.runtime, write(t, c.files...))
			t.Logf("EXPECTED: an explicit 127.0.0.1 bind, no 0.0.0.0\nACTUAL: %q", got)
			if strings.Contains(got, "0.0.0.0") || !strings.Contains(got, "127.0.0.1") {
				t.Errorf("PROBLEM CONFIRMED: %s start command %q does not bind loopback only", c.name, got)
			}
			// Control: the port is still passed through.
			if !strings.Contains(got, "${PORT}") {
				t.Errorf("start command %q lost ${PORT}", got)
			}
		})
	}
}

func TestScaffoldedDemosBindLoopback(t *testing.T) {
	for _, rt := range []Runtime{RuntimeNode, RuntimePython, RuntimeRuby, RuntimeGo} {
		t.Run(string(rt), func(t *testing.T) {
			files, _ := demoFiles(&App{Name: "demo", Runtime: rt})
			if len(files) == 0 {
				t.Fatalf("no demo files for %s", rt)
			}
			for name, content := range files {
				if name == "README.md" {
					continue
				}
				bad := strings.Contains(content, "0.0.0.0") || strings.Contains(content, `ListenAndServe(":"`)
				t.Logf("EXPECTED: %s binds 127.0.0.1 unless HOST says otherwise\nACTUAL: 0.0.0.0/all-interfaces bind present = %v", name, bad)
				if bad {
					t.Errorf("PROBLEM CONFIRMED: %s demo %s binds every interface", rt, name)
				}
				if !strings.Contains(content, "127.0.0.1") {
					t.Errorf("%s demo %s has no loopback default", rt, name)
				}
			}
		})
	}
}
