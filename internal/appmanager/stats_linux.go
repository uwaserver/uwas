package appmanager

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// readProcessStats reads CPU and memory stats from /proc on Linux.
// Returns (cpuPercent, rssBytes, vmsBytes). Best-effort: returns zeroes on error.
func readProcessStats(pid int) (cpuPct float64, rss, vms int64) {
	// Read RSS and VMS from /proc/[pid]/statm (pages)
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

	// Read CPU from /proc/[pid]/stat — field 14 (utime) + field 15 (stime)
	// Expressed as clock ticks since process start. We compute a rough percentage
	// by comparing to wall-clock elapsed time from field 22 (starttime).
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statData, err := os.ReadFile(statPath)
	if err != nil {
		return 0, rss, vms
	}

	// Fields after the comm field (which may contain spaces/parens)
	// Find closing paren, then split the rest
	closeIdx := strings.LastIndex(string(statData), ")")
	if closeIdx < 0 || closeIdx+2 >= len(statData) {
		return 0, rss, vms
	}
	rest := strings.Fields(string(statData)[closeIdx+2:])
	// rest[0] = state (index 2 in original), rest[11] = utime (14), rest[12] = stime (15), rest[19] = starttime (21)
	if len(rest) < 20 {
		return 0, rss, vms
	}

	utime, _ := strconv.ParseFloat(rest[11], 64)
	stime, _ := strconv.ParseFloat(rest[12], 64)
	starttime, _ := strconv.ParseFloat(rest[19], 64)

	// Read system uptime
	uptimeData, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, rss, vms
	}
	uptimeFields := strings.Fields(string(uptimeData))
	if len(uptimeFields) < 1 {
		return 0, rss, vms
	}
	systemUptime, _ := strconv.ParseFloat(uptimeFields[0], 64)

	// Clock ticks per second (usually 100 on Linux)
	clkTck := float64(100)
	totalTime := utime + stime
	elapsedSec := systemUptime - (starttime / clkTck)
	if elapsedSec > 0 {
		cpuPct = (totalTime / clkTck / elapsedSec) * 100.0
	}

	return cpuPct, rss, vms
}
