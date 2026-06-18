//go:build linux

package apps

import (
	"os"
	"testing"
)

// TestReadProcStatsSelf reads /proc for the test process itself, which always
// exists, exercising the full statm/stat/uptime parse path. CPU% may be 0 if
// the process has used negligible CPU; RSS should be non-zero.
func TestReadProcStatsSelf(t *testing.T) {
	cpu, rss, vms := readProcStats(os.Getpid())
	if rss <= 0 {
		t.Fatalf("self RSS should be > 0, got %d", rss)
	}
	if vms <= 0 {
		t.Fatalf("self VMS should be > 0, got %d", vms)
	}
	if cpu < 0 {
		t.Fatalf("CPU%% should be non-negative, got %f", cpu)
	}
}

// TestReadProcStatsMissingPID exercises the early-return-on-read-failure path
// using a PID that does not exist.
func TestReadProcStatsMissingPID(t *testing.T) {
	cpu, rss, vms := readProcStats(-1)
	if cpu != 0 || rss != 0 || vms != 0 {
		t.Fatalf("missing pid should yield zeroes, got %f %d %d", cpu, rss, vms)
	}
}

// TestReadDockerStatsUnavailable drives readDockerStats against a container id
// that does not exist; docker returns an error/empty so the function returns
// zeroes.
func TestReadDockerStatsUnavailable(t *testing.T) {
	cpu, mem := readDockerStats("uwas-app-nonexistent-stats-xyz")
	if cpu != 0 || mem != 0 {
		t.Fatalf("unavailable docker stats should be zero, got %f %d", cpu, mem)
	}
}

// TestParseHumanSizeBareSuffix covers the "B" suffix and bad-number branches
// not hit by the existing parser test.
func TestParseHumanSizeBareSuffix(t *testing.T) {
	if got := parseHumanSize("xMiB"); got != 0 {
		t.Fatalf("non-numeric mantissa should be 0, got %d", got)
	}
	if got := parseHumanSize("7B"); got != 7 {
		t.Fatalf("7B = %d, want 7", got)
	}
}
