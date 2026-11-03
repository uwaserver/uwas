package rlimit

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCPUQuotaRejectsOverflowBeforeEffects(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	effects := 0
	quota := ""
	osMkdirAllFn = func(string, os.FileMode) error { effects++; return nil }
	osReadFileFn = func(string) ([]byte, error) { return []byte("cpu memory pids"), nil }
	osWriteFileFn = func(p string, b []byte, _ os.FileMode) error {
		effects++
		if filepath.Base(p) == "cpu.max" {
			quota = string(b)
		}
		return nil
	}
	osStatFn = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	for _, tc := range []struct {
		percent int
		want    string
	}{{1, "1000 100000"}, {50, "50000 100000"}, {2147483647, "2147483647000 100000"}} {
		if _, err := Apply("valid.test", Limits{CPUPercent: tc.percent}); err != nil || quota != tc.want {
			t.Fatalf("%d: %v %q", tc.percent, err, quota)
		}
	}
	if strconv.IntSize == 64 {
		max := int64(1<<63 - 1)
		if _, err := Apply("boundary.test", Limits{CPUPercent: int(max / 1000)}); err != nil || quota != "9223372036854775000 100000" {
			t.Fatal("boundary", err, quota)
		}
		for _, n := range []int64{max/1000 + 1, max} {
			effects = 0
			if _, err := Apply("overflow.test", Limits{CPUPercent: int(n), MemoryMB: 1}); err == nil || effects != 0 {
				t.Fatal("overflow accepted", err, effects)
			}
		}
		runtimeGOOS = func() string { return "windows" }
		effects = 0
		if _, err := Apply("noop.test", Limits{CPUPercent: int(max)}); err != nil || effects != 0 {
			t.Fatal("platform no-op changed")
		}
	}
	runtimeGOOS = func() string { return "linux" }
	effects = 0
	if path, err := Apply("empty.test", Limits{}); err != nil || path != "" || effects != 0 {
		t.Fatal("unlimited default changed")
	}
}
