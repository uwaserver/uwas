package siteuser

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentCreateUsersKeepAllMatchBlocks pins the serialization of the
// sshd_config read-modify-write: concurrent creates for different domains
// each append their own Match block, and no block may be lost to a
// read-modify-write race. Before sshdMu, two overlapping creates each read
// the same base and the second writer silently dropped the first user's
// block (a lost update).
func TestConcurrentCreateUsersKeepAllMatchBlocks(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	base := "# sshd config\nSubsystem sftp /usr/lib/openssh/sftp-server\n"
	if err := os.WriteFile(sshdFile, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	execCommandFn = fakeExecCommand
	runtimeGOOS = "linux"

	hosts := []string{"a.example", "b.example", "c.example", "d.example"}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, h := range hosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			<-start
			if _, _, err := CreateUser(tmp, host); err != nil {
				t.Errorf("create %s: %v", host, err)
			}
		}(h)
	}
	close(start)
	wg.Wait()

	final, err := os.ReadFile(sshdFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(final)
	for _, h := range hosts {
		want := "Match User " + domainToUsername(h)
		if !strings.Contains(content, want) {
			t.Errorf("Match block for %s lost in the concurrent creates", h)
		}
	}
}
