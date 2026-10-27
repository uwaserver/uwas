package phpmanager

// F29100: SetConfig/SetConfigRaw take a pointer into m.installations and, for
// an install with no php.ini yet, write inst.ConfigFile after findInstallPtr
// has released m.mu. A concurrent GetConfig (findInstall copies the struct
// under the read lock) reads the same field: a data race on every first-time
// config save.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestSetConfigNoIniDoesNotRaceWithReaders(t *testing.T) {
	root := t.TempDir()
	confd := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(confd, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(root, "php.ini")
	if err := os.WriteFile(ini, []byte("[PHP]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(testLogger())
	m.execCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "echo 'Scan this dir for additional .ini files => "+confd+"'")
	}
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: "/usr/bin/php", SAPI: "cgi-fcgi"}}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = m.SetConfig("8.4.19", "memory_limit", "256M")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = m.GetConfig("8.4.19")
		}()
	}
	close(start)
	wg.Wait()

	got, err := m.GetConfig("8.4.19")
	if err != nil {
		t.Fatal(err)
	}
	if got.MemoryLimit != "256M" {
		t.Fatalf("memory_limit = %q, want 256M", got.MemoryLimit)
	}
}
