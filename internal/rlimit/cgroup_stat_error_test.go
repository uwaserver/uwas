package rlimit

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestApplyUnlimitedStatErrors(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	writes := 0
	osWriteFileFn = func(string, []byte, os.FileMode) error { writes++; return nil }
	osMkdirAllFn = func(string, os.FileMode) error { t.Fatal("unlimited must not create"); return nil }
	for _, cause := range []error{os.ErrNotExist, os.ErrPermission, syscall.EIO} {
		osStatFn = func(path string) (os.FileInfo, error) { return nil, &os.PathError{Op: "stat", Path: path, Err: cause} }
		path, err := Apply("example.test", Limits{})
		if path != "" {
			t.Fatal(path)
		}
		if errors.Is(cause, os.ErrNotExist) {
			if err != nil {
				t.Fatal("absence", err)
			}
		} else if !errors.Is(err, cause) {
			t.Fatal("lost cause", cause, err)
		}
		if writes != 0 {
			t.Fatal("unexpected writes")
		}
	}
	osStatFn = func(string) (os.FileInfo, error) { return nil, nil }
	osReadFileFn = func(string) ([]byte, error) { return []byte("previous limit"), nil }
	path, err := Apply("example.test", Limits{})
	if err != nil || path != "/sys/fs/cgroup/uwas/example.test" || writes != 3 {
		t.Fatal("existing lift", path, err, writes)
	}
	writes = 0
	osReadFileFn = func(string) ([]byte, error) { return nil, os.ErrPermission }
	if _, err := Apply("example.test", Limits{}); !errors.Is(err, os.ErrPermission) || writes != 0 {
		t.Fatal("limit read", err, writes)
	}
	runtimeGOOS = func() string { return "other" }
	osStatFn = func(string) (os.FileInfo, error) { t.Fatal("nonlinux stat"); return nil, nil }
	if path, err := Apply("example.test", Limits{}); path != "" || err != nil {
		t.Fatal(path, err)
	}
	fmt.Println("FIX VERIFIED")
}
