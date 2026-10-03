package rlimit

// Regression tests for lifting limits: the Limits contract says "0 means
// unlimited", and limits are re-applied from config on every domain load
// (server -> SetDomainLimits) and take effect on the next PHP worker start
// (phpmanager -> rlimit.Apply). A limit lowered to 0 must therefore reset the
// cgroup's cap instead of leaving the previous value enforced.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCgroupFS is a stateful in-memory stand-in for the cgroup filesystem so
// a second Apply can observe what the first one wrote.
type fakeCgroupFS struct {
	dirs  map[string]bool
	files map[string]string
}

func newFakeCgroupFS() *fakeCgroupFS {
	return &fakeCgroupFS{dirs: map[string]bool{}, files: map[string]string{}}
}

func (f *fakeCgroupFS) install(t *testing.T) {
	t.Helper()
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		f.dirs[path] = true
		return nil
	}
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		f.files[name] = string(data)
		return nil
	}
	osReadFileFn = func(name string) ([]byte, error) {
		if v, ok := f.files[name]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
	osStatFn = func(name string) (os.FileInfo, error) {
		if f.dirs[name] {
			return fakeDirInfo{}, nil
		}
		return nil, os.ErrNotExist
	}
}

// fakeDirInfo satisfies os.FileInfo for the existence checks.
type fakeDirInfo struct{}

func (fakeDirInfo) Name() string       { return "fake" }
func (fakeDirInfo) Size() int64        { return 0 }
func (fakeDirInfo) Mode() os.FileMode  { return 0o755 | os.ModeDir }
func (fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (fakeDirInfo) IsDir() bool        { return true }
func (fakeDirInfo) Sys() any           { return nil }

func domainFilePath(domain, file string) string {
	return filepath.Join(cgroupBase, sanitizeDomain(domain), file)
}

func TestApplyLoweringAllLimitsToZeroLiftsExistingCaps(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	fs := newFakeCgroupFS()
	fs.install(t)

	if _, err := Apply("stale.example", Limits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100}); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}
	if got := fs.files[domainFilePath("stale.example", "cpu.max")]; got != "50000 100000" {
		t.Fatalf("setup: cpu.max = %q, want %q", got, "50000 100000")
	}

	if _, err := Apply("stale.example", Limits{}); err != nil {
		t.Fatalf("lowering Apply: %v", err)
	}
	want := map[string]string{
		"cpu.max":    "max 100000",
		"memory.max": "max",
		"pids.max":   "max",
	}
	for file, unlimited := range want {
		if got := fs.files[domainFilePath("stale.example", file)]; got != unlimited {
			t.Errorf("%s = %q after lowering to 0; want %q (the stale cap would stay enforced)", file, got, unlimited)
		}
	}
}

func TestApplyRemovingOneLimitLiftsOnlyThatCap(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	fs := newFakeCgroupFS()
	fs.install(t)

	if _, err := Apply("part.example", Limits{CPUPercent: 50, MemoryMB: 256}); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}
	// CPU limit removed entirely; memory lowered but still set.
	if _, err := Apply("part.example", Limits{MemoryMB: 128}); err != nil {
		t.Fatalf("lowering Apply: %v", err)
	}
	if got := fs.files[domainFilePath("part.example", "cpu.max")]; got != "max 100000" {
		t.Errorf("cpu.max = %q after removing the CPU limit; want %q", got, "max 100000")
	}
	if got := fs.files[domainFilePath("part.example", "memory.max")]; got != "134217728" {
		t.Errorf("memory.max = %q, want the lowered %q", got, "134217728")
	}
}

func TestApplyWithoutLimitsLeavesUncreatedCgroupsAlone(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	fs := newFakeCgroupFS()
	fs.install(t)

	// A limit-free domain with no existing cgroup must not create one:
	// worker starts call Apply on every boot.
	path, err := Apply("fresh.example", Limits{})
	if err != nil || path != "" {
		t.Fatalf("Apply(no limits, no cgroup) = %q, %v; want %q, nil", path, err, "")
	}
	if len(fs.files) != 0 {
		t.Fatalf("writes = %v; want none", fs.files)
	}

	// A fresh mem-only domain must not touch cpu.max: its controller is not
	// delegated, so the file legitimately does not exist and a blind write
	// would fail on a real host.
	if _, err := Apply("memonly.example", Limits{MemoryMB: 64}); err != nil {
		t.Fatalf("mem-only Apply: %v", err)
	}
	if _, ok := fs.files[domainFilePath("memonly.example", "cpu.max")]; ok {
		t.Fatalf("cpu.max written for a mem-only domain")
	}
	if got := fs.files[domainFilePath("memonly.example", "memory.max")]; got != "67108864" {
		t.Fatalf("memory.max = %q, want %q", got, "67108864")
	}
}
