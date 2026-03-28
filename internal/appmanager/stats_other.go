//go:build !linux

package appmanager

// readProcessStats is a no-op on non-Linux platforms.
func readProcessStats(pid int) (cpuPct float64, rss, vms int64) {
	return 0, 0, 0
}
