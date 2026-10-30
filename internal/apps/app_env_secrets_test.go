//go:build !windows

package apps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// F9300: a managed app runs tenant code, so it must not inherit UWAS's own
// secrets (admin key, DNS/cloud tokens) from the server's environment, while
// the app's explicitly configured env and the port variables stay.
func TestNativeAppDoesNotInheritUWASSecrets(t *testing.T) {
	t.Setenv("UWAS_ADMIN_KEY", "LEAK-ADMIN-KEY")
	t.Setenv("CLOUDFLARE_API_TOKEN", "LEAK-CF-TOKEN")
	t.Setenv("CONTROL_VISIBLE", "ok")

	dir := t.TempDir()
	script := "#!/bin/sh\nenv > env.out\nsleep 2\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, NewStore(t.TempDir()), nil)
	app := &App{Name: "env-secrets", Runtime: RuntimeCustom, Command: "sh run.sh", WorkDir: dir,
		Env: EnvFromPairs("EXPLICIT_APP_TOKEN", "explicit-kept")}
	if err := m.Register(app); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(app.Name); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(app.Name)

	var out string
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(filepath.Join(dir, "env.out")); err == nil && len(b) > 0 {
			out = string(b)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out == "" {
		t.Fatal("app wrote no environment dump")
	}
	for _, want := range []string{"CONTROL_VISIBLE=ok", "EXPLICIT_APP_TOKEN=explicit-kept", "UWAS_ASSIGNED_PORT=", "PORT="} {
		if !strings.Contains(out, want) {
			t.Errorf("app environment lost %q", want)
		}
	}
	for _, secret := range []string{"LEAK-ADMIN-KEY", "LEAK-CF-TOKEN"} {
		if strings.Contains(out, secret) {
			t.Errorf("app environment leaked %q", secret)
		}
	}
}
