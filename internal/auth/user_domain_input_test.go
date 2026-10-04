package auth

import (
	"fmt"
	"testing"
)

func domainInputSnapshotCase(t *testing.T, update bool) string {
	t.Helper()
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	domains := []string{"original.test"}
	user, err := m.CreateUser("snapshot", "", "secret123", RoleUser, domains)
	if err != nil {
		t.Fatal(err)
	}
	user.Domains[0] = "output-change.test"
	control, _ := m.GetUser("snapshot")
	fmt.Printf("CONTROL EXPECTED: original.test ACTUAL: %s\n", control.Domains[0])
	if control.Domains[0] != "original.test" {
		t.Fatal("control")
	}
	if update {
		domains = []string{"updated.test"}
		if err := m.UpdateUser("snapshot", &User{Domains: domains}); err != nil {
			t.Fatal(err)
		}
	}
	release, done := make(chan struct{}), make(chan struct{})
	go func() { <-release; domains[0] = "caller-change.test"; close(done) }()
	close(release)
	<-done
	got, ok := m.GetUser("snapshot")
	if !ok {
		t.Fatal("missing user")
	}
	return got.Domains[0]
}
func TestUserDomainInputOwnership(t *testing.T) {
	if got := domainInputSnapshotCase(t, false); got != "original.test" {
		t.Fatal(got)
	}
	if got := domainInputSnapshotCase(t, true); got != "updated.test" {
		t.Fatal(got)
	}
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	if _, err := m.CreateUser("empty", "", "secret123", RoleUser, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateUser("empty", &User{Domains: []string{}}); err != nil {
		t.Fatal(err)
	}
	if user, _ := m.GetUser("empty"); len(user.Domains) != 0 {
		t.Fatal("empty update not retained")
	}
}
