package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestSessionSnapshotOwnership(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	_, err := m.CreateUser("alice", "", "secret123", RoleUser, []string{"example.test"})
	if err != nil {
		t.Fatal("invalid setup:", err)
	}
	original, err := m.Authenticate("alice", "secret123")
	if err != nil {
		t.Fatal("invalid login:", err)
	}
	snapshot, err := m.ValidateSession(original.Token)
	if err != nil {
		t.Fatal("invalid control:", err)
	}
	fmt.Println("CONTROL EXPECTED: valid session ACTUAL: valid session")
	release, done := make(chan struct{}), make(chan struct{})
	go func() { <-release; snapshot.ExpiresAt = time.Unix(0, 0); close(done) }()
	close(release)
	<-done
	_, err = m.ValidateSession(original.Token)
	fmt.Printf("EXPECTED: valid stored session ACTUAL: error=%v\n", err)
	if err != nil {
		fmt.Println("PROBLEM CONFIRMED")
		t.Fatal("returned session aliases owned state")
	}
	original.Domains[0] = "changed.test"
	original.UserID = "changed-id"
	checked, err := m.ValidateSession(original.Token)
	if err != nil {
		t.Fatal(err)
	}
	if checked.UserID == "changed-id" || checked.Domains[0] != "example.test" {
		t.Fatal("Authenticate returned owned state")
	}
	checked.Domains[0] = "changed-again.test"
	fresh, err := m.ValidateSession(original.Token)
	if err != nil || fresh.Domains[0] != "example.test" {
		t.Fatal("ValidateSession domains alias", err)
	}
	m.Logout(original.Token)
	if _, err := m.ValidateSession(original.Token); err == nil {
		t.Fatal("logout did not revoke snapshot token")
	}
	fmt.Println("FIX VERIFIED")
}
