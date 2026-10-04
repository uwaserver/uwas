package apps

import "testing"

func TestInstanceEnvironmentSnapshotOwnership(t *testing.T) {
	store := NewStore(t.TempDir())
	store.DataRoot = store.Dir
	m := NewManager(store, nil)
	if err := m.Register(&App{Name: "snapshot", Runtime: RuntimeCustom, Port: 43210, Env: EnvFromPairs("KEY", "original")}); err != nil {
		t.Fatal(err)
	}
	snapshot := m.Get("snapshot")
	release, done := make(chan struct{}), make(chan struct{})
	go func() { <-release; snapshot.Env["KEY"] = "changed"; close(done) }()
	close(release)
	<-done
	if got := m.Get("snapshot").Env["KEY"]; got != "original" {
		t.Fatalf("Get returned owned environment: %q", got)
	}
	instances := m.Instances()
	delete(instances[0].Env, "KEY")
	if got := m.Get("snapshot").Env["KEY"]; got != "original" {
		t.Fatalf("Instances returned owned environment: %q", got)
	}
	if err := m.Register(&App{Name: "empty", Runtime: RuntimeCustom, Port: 43211}); err != nil {
		t.Fatal(err)
	}
	if m.Get("empty").Env != nil {
		t.Fatal("nil environment must remain nil")
	}
}
