package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runInstallWithEnvWrite(t *testing.T, envErr, laterErr, removeErr error) ([]string, error) {
	t.Helper()
	origExec, origGOOS, origUID := installExecCommand, installRuntimeGOOS, installOsGetuid
	origExe, origWrite, origRead := installOsExecutable, installOsWriteFile, installOsReadFile
	origRename, origRemove := installOsRename, installOsRemove
	origStat, origMkdir := installOsStat, installOsMkdirAll
	origTTY := installIsTTY
	t.Cleanup(func() {
		installExecCommand, installRuntimeGOOS, installOsGetuid = origExec, origGOOS, origUID
		installOsExecutable, installOsWriteFile, installOsReadFile = origExe, origWrite, origRead
		installOsRename, installOsRemove = origRename, origRemove
		installOsStat, installOsMkdirAll = origStat, origMkdir
		installIsTTY = origTTY
	})
	t.Setenv("HOME", t.TempDir())
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	installOsExecutable = func() (string, error) { return "/bin/true", nil }
	installOsMkdirAll = func(string, os.FileMode) error { return nil }
	installOsStat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	installOsReadFile = func(name string) ([]byte, error) {
		if name == "" || strings.HasSuffix(name, "/true") || strings.HasSuffix(name, "/gnutrue") {
			return []byte("binary"), nil
		}
		return nil, os.ErrNotExist
	}
	installOsRename = func(string, string) error { return nil }
	var removed []string
	installOsRemove = func(name string) error {
		removed = append(removed, name)
		return removeErr
	}
	installIsTTY = func() bool { return false }
	installExecCommand = func(string, ...string) *exec.Cmd { return exec.Command("true") }
	installOsWriteFile = func(name string, _ []byte, _ os.FileMode) error {
		switch filepath.Clean(name) {
		case "/etc/uwas/.env":
			return envErr
		case "/etc/systemd/system/uwas.service":
			return laterErr
		default:
			return nil
		}
	}
	err := installUWAS([]string{"--no-start", "--yes"})
	return removed, err
}

func TestInstallReportsEnvWriteFailureAndRemovesPartialConfig(t *testing.T) {
	errEnv := errors.New("env write failed")
	errLater := errors.New("later service write")
	removed, err := runInstallWithEnvWrite(t, errEnv, errLater, nil)
	if !errors.Is(err, errEnv) || errors.Is(err, errLater) {
		t.Fatalf("install error = %v, want env write error", err)
	}
	if len(removed) != 1 || filepath.Clean(removed[0]) != "/etc/uwas/uwas.yaml" {
		t.Fatalf("removed = %v, want partial config", removed)
	}
}
