package phpmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// php-cgi workers run tenant PHP code, which can read the worker environment
// with getenv(). They used to inherit the whole UWAS environment, handing every
// site secrets such as the admin API key supplied as ${UWAS_ADMIN_KEY}.
func TestPHPWorkersDoNotInheritUWASSecrets(t *testing.T) {
	t.Setenv("UWAS_ADMIN_KEY", "worker-env-secret")
	t.Setenv("LANG", "C.UTF-8")

	start := map[string]func(m *Manager) (stop func(), err error){
		"StartDomain": func(m *Manager) (func(), error) {
			if _, err := m.AssignDomain("tenant.test", "8.4"); err != nil {
				return nil, err
			}
			return func() { _ = m.StopDomain("tenant.test") }, m.StartDomain("tenant.test")
		},
		"StartFPM": func(m *Manager) (func(), error) {
			return func() { _ = m.StopFPM("8.4") }, m.StartFPM("8.4", "127.0.0.1:0")
		},
	}
	for name, startFn := range start {
		t.Run(name, func(t *testing.T) {
			dump := filepath.Join(t.TempDir(), "env")
			m := New(testLogger())
			// No sibling php-fpm binary, so StartFPM takes the php-cgi path.
			m.installations = []PHPInstall{{Version: "8.4.19", Binary: filepath.Join(t.TempDir(), "php-cgi"), SAPI: "cgi-fcgi"}}
			m.execCommand = func(string, ...string) *exec.Cmd {
				return exec.Command("sh", "-c", `env > "$0.tmp" && mv "$0.tmp" "$0"; exec sleep 100`, dump)
			}
			stop, err := startFn(m)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			t.Cleanup(stop)

			var env string
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				if b, err := os.ReadFile(dump); err == nil {
					env = string(b)
					break
				}
			}
			if env == "" {
				t.Fatal("worker never wrote its environment")
			}
			if strings.Contains(env, "UWAS_ADMIN_KEY=") {
				t.Error("UWAS_ADMIN_KEY reached the PHP worker environment")
			}
			for _, want := range []string{"PHP_FCGI_CHILDREN=8", "LANG=C.UTF-8", "PATH="} {
				if !strings.Contains(env, want) {
					t.Errorf("worker environment is missing %q", want)
				}
			}
		})
	}
}
