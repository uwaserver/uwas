//go:build linux

package apps

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// readProcStats reads /proc/<pid>/{statm,stat} and computes a CPU
// percentage by comparing accumulated CPU time to wall-clock elapsed
// since the process started. Returns zeroes on any read failure —
// callers treat absent stats as "unknown" rather than an error.
//
// Same algorithm as internal/appmanager/stats_linux.go; kept separate
// so the apps package doesn't depend on the legacy supervisor that
// it's replacing.
func readProcStats(pid int) (cpuPct float64, rss, vms int64) {
	statmPath := fmt.Sprintf("/proc/%d/statm", pid)
	data, err := os.ReadFile(statmPath)
	if err != nil {
		return 0, 0, 0
	}

	fields := strings.Fields(string(data))
	if len(fields) >= 2 {
		pageSize := int64(os.Getpagesize())
		if v, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
			vms = v * pageSize
		}
		if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			rss = v * pageSize
		}
	}

	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statData, err := os.ReadFile(statPath)
	if err != nil {
		return 0, rss, vms
	}

	// The comm field can contain spaces/parens, so split after the
	// last closing paren — same trick the kernel docs recommend.
	closeIdx := strings.LastIndex(string(statData), ")")
	if closeIdx < 0 || closeIdx+2 >= len(statData) {
		return 0, rss, vms
	}
	rest := strings.Fields(string(statData)[closeIdx+2:])
	if len(rest) < 20 {
		return 0, rss, vms
	}

	utime, err := strconv.ParseFloat(rest[11], 64)
	if err != nil {
		return 0, rss, vms
	}
	stime, err := strconv.ParseFloat(rest[12], 64)
	if err != nil {
		return 0, rss, vms
	}
	starttime, err := strconv.ParseFloat(rest[19], 64)
	if err != nil {
		return 0, rss, vms
	}

	uptimeData, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, rss, vms
	}
	uptimeFields := strings.Fields(string(uptimeData))
	if len(uptimeFields) < 1 {
		return 0, rss, vms
	}
	systemUptime, err := strconv.ParseFloat(uptimeFields[0], 64)
	if err != nil {
		return 0, rss, vms
	}

	const clkTck = 100.0
	totalTime := utime + stime
	elapsedSec := systemUptime - (starttime / clkTck)
	if elapsedSec > 0 {
		cpuPct = (totalTime / clkTck / elapsedSec) * 100.0
	}
	return cpuPct, rss, vms
}

// readDockerStats invokes `docker stats --no-stream` for a single
// container and returns (cpuPct, memBytes). Docker formats memory as
// "12.34MiB / 1.2GiB" — we parse the left side. CPU is a percentage
// of one CPU core (so a 2-core-pegged container reports 200%).
//
// Best-effort: returns zeroes if the docker CLI or daemon is
// unavailable.
func readDockerStats(containerID string) (cpuPct float64, mem int64) {
	cmd := exec.Command("docker", "stats", "--no-stream",
		"--format", "{{.CPUPerc}}|{{.MemUsage}}", containerID)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, 0
	}
	line := strings.TrimSpace(out.String())
	if line == "" {
		return 0, 0
	}
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return 0, 0
	}

	// "12.34%" → 12.34
	cpuStr := strings.TrimSuffix(strings.TrimSpace(parts[0]), "%")
	if v, err := strconv.ParseFloat(cpuStr, 64); err == nil {
		cpuPct = v
	}

	// "12.34MiB / 1.2GiB" — parse left side.
	memStr := strings.TrimSpace(parts[1])
	if i := strings.Index(memStr, " "); i > 0 {
		memStr = memStr[:i]
	}
	mem = parseHumanSize(memStr)
	return cpuPct, mem
}

// parseHumanSize handles the docker-CLI memory format ("12.34MiB",
// "456KiB", "1.2GiB"). Returns bytes; 0 on parse failure.
func parseHumanSize(s string) int64 {
	if s == "" {
		return 0
	}
	multipliers := []struct {
		suffix string
		mul    float64
	}{
		{"TiB", 1024 * 1024 * 1024 * 1024},
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"TB", 1000 * 1000 * 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"kB", 1000},
		{"B", 1},
	}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			num := strings.TrimSuffix(s, m.suffix)
			if v, err := strconv.ParseFloat(num, 64); err == nil {
				return int64(v * m.mul)
			}
			return 0
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(v)
	}
	return 0
}
