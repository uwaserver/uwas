package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func userPersistFailureBlocked(t *testing.T, kind string, bootstrap bool) (bool, bool) {
	t.Helper()
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	if kind == "mkdir" {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		m.dataDir = filepath.Join(blocker, "data")
	} else {
		if err := os.Mkdir(m.usersFile(), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if bootstrap {
		_, err = m.CreateFirstAdmin("pending", "", "secret123")
	} else {
		_, err = m.CreateUser("pending", "", "secret123", RoleUser, nil)
	}
	_, present := m.GetUser("pending")
	return err != nil, present
}
func TestUserCreationRollsBackPersistenceFailure(t *testing.T) {
	for _, kind := range []string{"mkdir", "write"} {
		for _, bootstrap := range []bool{false, true} {
			failed, present := userPersistFailureBlocked(t, kind, bootstrap)
			if !failed || present {
				t.Fatal(kind, bootstrap, failed, present)
			}
		}
	}
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	if err := os.Mkdir(m.usersFile(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateFirstAdmin("pending", "", "secret123"); err == nil {
		t.Fatal("blocked bootstrap succeeded")
	}
	if len(m.usersByID) != 0 || len(m.usersByAPIKeyHash) != 0 {
		t.Fatal("secondary indexes retained failed account")
	}
	if err := os.Remove(m.usersFile()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateFirstAdmin("pending", "", "secret123"); err != nil {
		t.Fatal("retry", err)
	}
	m.dataDir = ""
	if _, err := m.CreateUser("ephemeral", "", "secret123", RoleUser, nil); err != nil {
		t.Fatal("ephemeral", err)
	}
}
