package apps

import (
	"fmt"
	"testing"
)

func portIdentityManager(t *testing.T) *Manager {
	t.Helper()
	s := NewStore(t.TempDir())
	s.DataRoot = s.Dir
	m := NewManager(s, nil)
	if err := m.Register(&App{Name: "adopt", Runtime: RuntimeCustom, Port: 43210}); err != nil {
		t.Fatal(err)
	}
	return m
}
func portIdentityStale(t *testing.T, removed bool) (int, bool) {
	t.Helper()
	m := portIdentityManager(t)
	old := m.procs["adopt"]
	if err := m.adoptDiscoveredPort(old, 43211); err != nil {
		t.Fatal("control", err)
	}
	saved, err := m.store.Get("adopt")
	if err != nil || saved.Port != 43211 {
		t.Fatal("control persistence")
	}
	fmt.Printf("CONTROL EXPECTED: 43211 ACTUAL: %d\n", saved.Port)
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() { close(entered); <-release; result <- m.adoptDiscoveredPort(old, 43213) }()
	<-entered
	if removed {
		m.mu.Lock()
		delete(m.procs, "adopt")
		m.mu.Unlock()
	} else {
		if err := m.Register(&App{Name: "adopt", Runtime: RuntimeCustom, Port: 43212}); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	adoptErr := <-result
	saved, err = m.store.Get("adopt")
	if err != nil {
		t.Fatal(err)
	}
	return saved.Port, adoptErr != nil
}
func TestDiscoveredPortRequiresCurrentProcess(t *testing.T) {
	for _, removed := range []bool{false, true} {
		got, rejected := portIdentityStale(t, removed)
		want := 43212
		if removed {
			want = 43211
		}
		if got != want || !rejected {
			t.Fatalf("port=%d rejected=%v", got, rejected)
		}
	}
	m := portIdentityManager(t)
	if m.adoptDiscoveredPort(nil, 43213) == nil {
		t.Fatal("nil accepted")
	}
	if err := m.adoptDiscoveredPort(m.procs["adopt"], 43210); err != nil {
		t.Fatal("current same-port", err)
	}
}
