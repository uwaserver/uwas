// Package rlimit manages per-domain resource limits using Linux cgroups v2.
// On non-Linux platforms, all operations are no-ops.
package rlimit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const cgroupBase = "/sys/fs/cgroup/uwas"

// Testable hooks can be overridden in tests.
var (
	osMkdirAllFn  = os.MkdirAll
	osWriteFileFn = os.WriteFile
	osReadFileFn  = os.ReadFile
	osRemoveFn    = os.Remove
	runtimeGOOS   = func() string { return runtime.GOOS }
)

// Limits defines resource constraints for a domain.
type Limits struct {
	// CPUPercent is the max CPU usage as a percentage (e.g. 50 = 50% of one core).
	// 0 means unlimited.
	CPUPercent int `yaml:"cpu_percent,omitempty" json:"cpu_percent,omitempty"`
	// MemoryMB is the max memory in megabytes. 0 means unlimited.
	MemoryMB int `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty"`
	// PIDMax is the max number of processes. 0 means unlimited.
	PIDMax int `yaml:"pid_max,omitempty" json:"pid_max,omitempty"`
}

// Apply creates/updates a cgroup for the given domain with the specified limits.
// Returns the cgroup path so a process can be assigned to it. No-op on non-Linux.
func Apply(domain string, limits Limits) (cgroupPath string, err error) {
	if runtimeGOOS() != "linux" {
		return "", nil
	}
	if limits.CPUPercent == 0 && limits.MemoryMB == 0 && limits.PIDMax == 0 {
		return "", nil
	}

	if err := osMkdirAllFn(cgroupBase, 0755); err != nil {
		return "", fmt.Errorf("create cgroup %s: %w", cgroupBase, err)
	}
	// A child cgroup only gets cpu.max / memory.max / pids.max if the parent
	// enables those controllers for its children. Without this the child is
	// created with nothing but cgroup.* and *.pressure files, and every write
	// below fails with EACCES — the limits silently do not exist.
	if err := enableControllers(cgroupBase, limits); err != nil {
		return "", err
	}

	path := filepath.Join(cgroupBase, sanitizeDomain(domain))
	if err := osMkdirAllFn(path, 0755); err != nil {
		return "", fmt.Errorf("create cgroup %s: %w", path, err)
	}

	if limits.CPUPercent > 0 {
		quota := limits.CPUPercent * 1000
		val := fmt.Sprintf("%d 100000", quota)
		if err := osWriteFileFn(filepath.Join(path, "cpu.max"), []byte(val), 0644); err != nil {
			return path, fmt.Errorf("set cpu.max: %w", err)
		}
	}

	if limits.MemoryMB > 0 {
		val := strconv.FormatInt(int64(limits.MemoryMB)*1024*1024, 10)
		if err := osWriteFileFn(filepath.Join(path, "memory.max"), []byte(val), 0644); err != nil {
			return path, fmt.Errorf("set memory.max: %w", err)
		}
	}

	if limits.PIDMax > 0 {
		val := strconv.Itoa(limits.PIDMax)
		if err := osWriteFileFn(filepath.Join(path, "pids.max"), []byte(val), 0644); err != nil {
			return path, fmt.Errorf("set pids.max: %w", err)
		}
	}

	return path, nil
}

// AssignPID moves a process into the domain's cgroup.
func AssignPID(cgroupPath string, pid int) error {
	if runtimeGOOS() != "linux" || cgroupPath == "" {
		return nil
	}
	procsFile := filepath.Join(cgroupPath, "cgroup.procs")
	return osWriteFileFn(procsFile, []byte(strconv.Itoa(pid)), 0644)
}

// Remove deletes the cgroup for a domain.
func Remove(domain string) error {
	if runtimeGOOS() != "linux" {
		return nil
	}
	path := filepath.Join(cgroupBase, sanitizeDomain(domain))
	return osRemoveFn(path)
}

// sanitizeDomain converts a domain name to a safe cgroup directory name.
func sanitizeDomain(domain string) string {
	safe := make([]byte, 0, len(domain))
	for _, c := range []byte(domain) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			safe = append(safe, c)
		} else if c >= 'A' && c <= 'Z' {
			safe = append(safe, c+32)
		} else {
			safe = append(safe, '_')
		}
	}
	return string(safe)
}

// controllersFor returns the cgroup v2 controllers the given limits need.
func controllersFor(limits Limits) []string {
	var out []string
	if limits.CPUPercent > 0 {
		out = append(out, "cpu")
	}
	if limits.MemoryMB > 0 {
		out = append(out, "memory")
	}
	if limits.PIDMax > 0 {
		out = append(out, "pids")
	}
	return out
}

// enableControllers delegates the controllers a domain needs to the children
// of base, writing only the ones that are not already enabled.
//
// The kernel refuses a controller that the base cgroup itself was not granted
// (its own parent must list it in cgroup.subtree_control). That is reported
// rather than silently producing a cgroup with no limit files in it.
func enableControllers(base string, limits Limits) error {
	need := controllersFor(limits)
	if len(need) == 0 {
		return nil
	}

	enabled := map[string]bool{}
	if data, err := osReadFileFn(filepath.Join(base, "cgroup.subtree_control")); err == nil {
		for _, c := range strings.Fields(string(data)) {
			enabled[c] = true
		}
	}

	var missing []string
	for _, c := range need {
		if !enabled[c] {
			missing = append(missing, "+"+c)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	target := filepath.Join(base, "cgroup.subtree_control")
	if err := osWriteFileFn(target, []byte(strings.Join(missing, " ")), 0644); err != nil {
		return fmt.Errorf("enable cgroup controllers %s in %s: %w (is cgroup v2 mounted, and are these controllers delegated to %s?)",
			strings.Join(missing, " "), target, err, base)
	}
	return nil
}
