package auth

import (
	"os"
	"testing"
)

// F1210: delete / disable / password change / key rotation must report a
// failed users.json write instead of returning nil, or the revocation is
// silently undone by the next restart.
func blockedUsersManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	if _, err := m.CreateUser("bob", "", "old-password-1", RoleUser, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.usersFile()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	return m, func() {
		if err := os.Remove(m.usersFile() + ".tmp"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMutationsReportPersistFailure(t *testing.T) {
	t.Run("DeleteUser", func(t *testing.T) {
		m, unblock := blockedUsersManager(t)
		if err := m.DeleteUser("bob"); err == nil {
			t.Fatal("DeleteUser hid a failed write")
		}
		if _, ok := m.GetUser("bob"); ok {
			t.Error("deletion must stay effective in memory")
		}
		unblock()
		if err := m.DeleteUser("bob"); err == nil {
			t.Error("second delete of a missing user should fail")
		}
	})
	t.Run("ChangePassword", func(t *testing.T) {
		m, _ := blockedUsersManager(t)
		if err := m.ChangePassword("bob", "old-password-1", "new-password-2"); err == nil {
			t.Fatal("ChangePassword hid a failed write")
		}
		if err := m.ChangePassword("bob", "wrong", "x-password-3"); err == nil {
			t.Error("wrong current password must still be rejected")
		}
	})
	t.Run("UpdateUserDisable", func(t *testing.T) {
		m, _ := blockedUsersManager(t)
		if err := m.UpdateUser("bob", &User{EnabledSet: true, Enabled: false}); err == nil {
			t.Fatal("UpdateUser hid a failed write")
		}
		if u, _ := m.GetUser("bob"); u == nil || u.Enabled {
			t.Error("disable must stay effective in memory")
		}
		if err := m.UpdateUser("nobody", &User{Email: "x"}); err == nil {
			t.Error("unknown user must fail")
		}
	})
	t.Run("RegenerateAPIKeyRollsBack", func(t *testing.T) {
		m, unblock := blockedUsersManager(t)
		before, _ := m.GetUser("bob")
		oldHash := before.APIKeyHash
		if key, err := m.RegenerateAPIKey("bob"); err == nil || key != "" {
			t.Fatalf("RegenerateAPIKey = %q, %v; want error", key, err)
		}
		after, _ := m.GetUser("bob")
		if after.APIKeyHash != oldHash || after.APIKey != before.APIKey {
			t.Error("failed rotation must restore the previous key")
		}
		if m.usersByAPIKeyHash[oldHash] == nil || len(m.usersByAPIKeyHash) != 1 {
			t.Errorf("api key index not restored: %d entries", len(m.usersByAPIKeyHash))
		}
		unblock()
		if key, err := m.RegenerateAPIKey("bob"); err != nil || key == "" {
			t.Fatalf("rotation after unblock: %q, %v", key, err)
		}
	})
}
