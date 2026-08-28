//go:build linux

package rlimit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These run against the real kernel, not the hooks. They are the only proof
// that Apply produces a cgroup the kernel actually enforces: the hook-based
// tests happily passed while Apply created a cgroup with no cpu.max,
// memory.max or pids.max in it at all, because it never delegated the
// controllers through the parent's cgroup.subtree_control.
//
// Requires cgroup v2 and root. Run them with:
//
//	docker run --rm --privileged --cgroupns=host -v "$PWD":/src -w /src \
//	  golang:1.26 go test ./internal/rlimit/ -run Kernel -v

func requireCgroup2Root(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("cgroup v2 yazımı root gerektirir")
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Skip("cgroup v2 bağlı değil")
	}
}

func TestKernelApplyEnforcesLimits(t *testing.T) {
	requireCgroup2Root(t)

	const domain = "kernel-test.example"
	t.Cleanup(func() { _ = Remove(domain) })

	path, err := Apply(domain, Limits{CPUPercent: 50, MemoryMB: 100, PIDMax: 42})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if path == "" {
		t.Fatal("Apply boş yol döndürdü")
	}

	// Read the values back from the kernel, not from a recording hook.
	want := map[string]string{
		"cpu.max":    "50000 100000",
		"memory.max": "104857600",
		"pids.max":   "42",
	}
	for file, expected := range want {
		data, err := os.ReadFile(filepath.Join(path, file))
		if err != nil {
			t.Errorf("%s okunamadı: %v — denetleyici üst cgroup'a devredilmemiş", file, err)
			continue
		}
		if got := strings.TrimSpace(string(data)); got != expected {
			t.Errorf("%s = %q, want %q", file, got, expected)
		}
	}
}

// A process assigned to the cgroup must actually appear in it.
func TestKernelAssignPIDMovesProcess(t *testing.T) {
	requireCgroup2Root(t)

	const domain = "kernel-assign.example"
	t.Cleanup(func() { _ = Remove(domain) })

	path, err := Apply(domain, Limits{PIDMax: 64})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("süreç başlatılamadı: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	if err := AssignPID(path, cmd.Process.Pid); err != nil {
		t.Fatalf("AssignPID: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
	if err != nil {
		t.Fatalf("cgroup.procs okunamadı: %v", err)
	}
	found := false
	for _, line := range strings.Fields(string(data)) {
		if line == itoa(cmd.Process.Pid) {
			found = true
		}
	}
	if !found {
		t.Errorf("PID %d cgroup'ta yok; üyeler: %q", cmd.Process.Pid, strings.TrimSpace(string(data)))
	}
}

// The memory limit must be enforced, not merely written: a process that asks
// for more than its cap must be killed by the kernel.
func TestKernelMemoryLimitIsEnforced(t *testing.T) {
	requireCgroup2Root(t)

	const domain = "kernel-oom.example"
	t.Cleanup(func() { _ = Remove(domain) })

	path, err := Apply(domain, Limits{MemoryMB: 16})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Without this the kernel swaps instead of killing, and the test hangs.
	if err := os.WriteFile(filepath.Join(path, "memory.swap.max"), []byte("0"), 0o644); err != nil {
		t.Skipf("memory.swap.max ayarlanamadı: %v", err)
	}

	// Allocate well past the cap. sh reads the whole string into memory.
	cmd := exec.Command("sh", "-c", `exec sh -c 'A=""; while :; do A="$A$(head -c 1000000 /dev/zero | tr "\0" "x")"; done'`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("süreç başlatılamadı: %v", err)
	}
	if err := AssignPID(path, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("AssignPID: %v", err)
	}

	bitti := make(chan error, 1)
	go func() { bitti <- cmd.Wait() }()

	select {
	case err := <-bitti:
		// Killed by the OOM killer, or the shell died trying. Either way the
		// cap held; an unlimited process would still be running.
		t.Logf("süreç sonlandı: %v", err)
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-bitti
		t.Error("16MB sınırı altındaki süreç sınırsız bellek ayırmaya devam etti")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
