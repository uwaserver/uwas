package phpmanager

import (
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/rlimit"
)

// F24000: UnassignDomain removed a dead worker's cgroup after releasing
// domainMu. The cgroup is keyed by domain name, so a domain re-assigned and
// restarted in that gap lost the directory its new worker lives in. The fake
// rlimitRemove probes domainMu with TryLock: if the lock is free, the racing
// Assign+Start runs exactly there; if it is held, the racer can only run once
// Unassign has returned (gated order, no sleeps).
func runUnassignCgroupRace(t *testing.T, domain string, race bool) (cgroupExists, instLive bool) {
	t.Helper()
	var mu sync.Mutex
	cgroups := map[string]bool{}
	origA, origP, origR := rlimitApply, rlimitAssignPID, rlimitRemove
	defer func() { rlimitApply, rlimitAssignPID, rlimitRemove = origA, origP, origR }()

	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.27", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"}}
	var spawns atomic.Int32
	m.execCommand = func(string, ...string) *exec.Cmd {
		if spawns.Add(1) == 1 {
			return exec.Command("sh", "-c", "exit 1") // ilk worker ölür
		}
		return exec.Command("sh", "-c", "sleep 60") // yeniden atanan örnek yaşar
	}
	rlimitApply = func(d string, _ rlimit.Limits) (string, error) {
		mu.Lock()
		cgroups[d] = true
		mu.Unlock()
		return "/sys/fs/cgroup/uwas/" + d, nil
	}
	rlimitAssignPID = func(string, int) error { return nil }

	raced := false
	reassign := func() {
		if raced || !race {
			return
		}
		raced = true
		if _, err := m.AssignDomain(domain, "8.4"); err != nil {
			t.Errorf("reassign: %v", err)
			return
		}
		if err := m.StartDomain(domain); err != nil {
			t.Errorf("restart: %v", err)
		}
	}
	removeCalls := make(chan struct{}, 8)
	rlimitRemove = func(d string) error {
		defer func() { removeCalls <- struct{}{} }()
		if m.domainMu.TryLock() {
			m.domainMu.Unlock()
			reassign() // kilit serbest: yarışan istek bu pencerede girebilir
		}
		mu.Lock()
		delete(cgroups, d)
		mu.Unlock()
		return nil
	}

	done := make(chan struct{})
	m.SetOnCrash(func(d string) {
		m.UnassignDomain(d)
		reassign() // kilit tutuluyorsa yarışan istek ancak Unassign bittikten sonra girebilir
		close(done)
	})
	if _, err := m.AssignDomain(domain, "8.4"); err != nil {
		t.Fatal(err)
	}
	if err := m.StartDomain(domain); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("crash callback tetiklenmedi")
	}
	m.domainMu.RLock()
	if di, ok := m.domainMap[domain]; ok {
		instLive = race && di.cgroupPath != ""
	}
	m.domainMu.RUnlock()
	mu.Lock()
	cgroupExists = cgroups[domain]
	mu.Unlock()
	if race {
		m.UnassignDomain(domain) // temizlik: yaşayan sleep worker'ı öldür
		// Worker'ın monitor'ü çıkışta cgroup'u siler (Unassign canlı worker'a
		// dokunmaz); sahteleri geri yüklemeden önce o silmeyi kapıyla bekle.
		<-removeCalls // ölü worker için Unassign'ın silmesi
		<-removeCalls // canlı worker için monitor'ün silmesi
	} else {
		<-removeCalls
	}
	return
}

func TestUnassignKeepsReassignedDomainCgroup(t *testing.T) {
	// Özgün yeniden üretim: yeni örneğin cgroup'u silinmemeli.
	exists, live := runUnassignCgroupRace(t, "race.test", true)
	if !live || !exists {
		t.Fatalf("yeniden atanan örneğin cgroup'u kayboldu: live=%v exists=%v", live, exists)
	}
	// Kenar 1: yarış yokken ölü worker'ın cgroup'u hâlâ siliniyor (F23701 korunur).
	if exists, _ := runUnassignCgroupRace(t, "ctl.test", false); exists {
		t.Fatal("yarışsız Unassign cgroup'u silmedi (F23701 gerilemesi)")
	}
	// Kenar 2: tekrar — aynı yol üst üste çalışıyor.
	for i := 0; i < 2; i++ {
		if exists, live := runUnassignCgroupRace(t, "again.test", true); !live || !exists {
			t.Fatalf("tur %d: live=%v exists=%v", i, live, exists)
		}
	}
}
