package auth

import (
	"errors"
	"testing"
)

func enabledAdmins(m *Manager) int {
	n := 0
	for _, u := range m.ListUsers() {
		if u.Role == RoleAdmin && u.Enabled {
			n++
		}
	}
	return n
}

// F53301: two admins each pass the handler's "not the last admin" pre-check
// and then disable one another; the manager must refuse the call that would
// leave no enabled admin, atomically under its own lock.
func TestUpdateUserRefusesDisablingLastEnabledAdmin(t *testing.T) {
	m := newTestManager(t)
	for _, n := range []string{"adm-a", "adm-b"} {
		if _, err := m.CreateUser(n, "", "S3cure-Passw0rd!", RoleAdmin, nil); err != nil {
			t.Fatal(err)
		}
	}
	var lastErr error
	for _, u := range m.ListUsers() {
		if u.Role == RoleAdmin {
			lastErr = m.UpdateUserKeepAdmin(u.Username, &User{Enabled: false, EnabledSet: true})
		}
	}
	if enabledAdmins(m) != 1 {
		t.Fatalf("enabled admins = %d, want exactly 1", enabledAdmins(m))
	}
	if !errors.Is(lastErr, ErrLastAdmin) {
		t.Fatalf("err = %v, want ErrLastAdmin", lastErr)
	}
	// Control: disabling a non-admin or re-enabling is never refused.
	if _, err := m.CreateUser("plain", "", "S3cure-Passw0rd!", RoleUser, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateUser("plain", &User{Enabled: false, EnabledSet: true}); err != nil {
		t.Fatalf("non-admin disable: %v", err)
	}
}

func TestDeleteUserRefusesLastEnabledAdmin(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.CreateUser("adm-a", "", "S3cure-Passw0rd!", RoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	var err error
	for _, u := range m.ListUsers() {
		if u.Role == RoleAdmin {
			err = m.DeleteUserKeepAdmin(u.Username)
		}
	}
	if enabledAdmins(m) < 1 {
		t.Fatalf("all admins deleted; last err = %v", err)
	}
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("err = %v, want ErrLastAdmin", err)
	}
}

func TestUserNotFoundSentinel(t *testing.T) {
	m := newTestManager(t)
	if err := m.UpdateUser("ghost", &User{}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("update: %v", err)
	}
	if err := m.DeleteUser("ghost"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.RegenerateAPIKey("ghost"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("regen: %v", err)
	}
}
