package auth

import (
	"context"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	return NewManager(t.TempDir(), "test-global-api-key")
}

func TestCreateUser(t *testing.T) {
	m := newTestManager(t)

	user, err := m.CreateUser("alice", "alice@example.com", "secret123", RoleAdmin, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.Username != "alice" {
		t.Errorf("expected username alice, got %s", user.Username)
	}
	if user.Role != RoleAdmin {
		t.Errorf("expected role admin, got %s", user.Role)
	}
	if user.APIKey == "" {
		t.Error("expected API key to be generated")
	}
	if !user.Enabled {
		t.Error("expected user to be enabled")
	}
}

func TestCreateUserDuplicate(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "pass", RoleUser, nil)

	_, err := m.CreateUser("alice", "", "pass2", RoleUser, nil)
	if err == nil {
		t.Error("expected error for duplicate user")
	}
}

func TestCreateUserValidation(t *testing.T) {
	m := newTestManager(t)

	// Empty username
	_, err := m.CreateUser("", "", "pass", RoleUser, nil)
	if err == nil {
		t.Error("expected error for empty username")
	}

	// Empty password
	_, err = m.CreateUser("bob", "", "", RoleUser, nil)
	if err == nil {
		t.Error("expected error for empty password")
	}

	// Invalid role
	_, err = m.CreateUser("bob", "", "pass", "superadmin", nil)
	if err == nil {
		t.Error("expected error for invalid role")
	}
}

func TestAuthenticate(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	session, err := m.Authenticate("alice", "secret")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if session.Username != "alice" {
		t.Errorf("expected username alice in session, got %s", session.Username)
	}
	if session.Token == "" {
		t.Error("expected session token")
	}
}

func TestAuthenticateWrongPassword(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	_, err := m.Authenticate("alice", "wrong")
	if err == nil {
		t.Error("expected error for wrong password")
	}
}

func TestAuthenticateNonexistent(t *testing.T) {
	m := newTestManager(t)

	_, err := m.Authenticate("ghost", "pass")
	if err == nil {
		t.Error("expected error for nonexistent user")
	}
}

func TestValidateSession(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	session, _ := m.Authenticate("alice", "secret")

	validated, err := m.ValidateSession(session.Token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if validated.Username != "alice" {
		t.Errorf("expected alice, got %s", validated.Username)
	}
}

func TestValidateSessionInvalid(t *testing.T) {
	m := newTestManager(t)

	_, err := m.ValidateSession("fake-token")
	if err == nil {
		t.Error("expected error for invalid session")
	}
}

func TestLogout(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	session, _ := m.Authenticate("alice", "secret")

	m.Logout(session.Token)

	_, err := m.ValidateSession(session.Token)
	if err == nil {
		t.Error("expected error after logout")
	}
}

func TestAuthenticateAPIKey(t *testing.T) {
	m := newTestManager(t)
	user, _ := m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Per-user API key
	authenticated, err := m.AuthenticateAPIKey(user.APIKey)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if authenticated.Username != "alice" {
		t.Errorf("expected alice, got %s", authenticated.Username)
	}
}

func TestAuthenticateGlobalAPIKey(t *testing.T) {
	m := newTestManager(t)

	user, err := m.AuthenticateAPIKey("test-global-api-key")
	if err != nil {
		t.Fatalf("AuthenticateAPIKey (global): %v", err)
	}
	if user.Role != RoleAdmin {
		t.Errorf("expected admin role for global key, got %s", user.Role)
	}
}

func TestAuthenticateAPIKeyInvalid(t *testing.T) {
	m := newTestManager(t)

	_, err := m.AuthenticateAPIKey("invalid-key")
	if err == nil {
		t.Error("expected error for invalid API key")
	}
}

func TestHasPermission(t *testing.T) {
	m := newTestManager(t)

	tests := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleAdmin, PermDomainCreate, true},
		{RoleAdmin, PermUserDelete, true},
		{RoleAdmin, PermSystemConfig, true},
		{RoleReseller, PermDomainCreate, true},
		{RoleReseller, PermUserDelete, false},
		{RoleReseller, PermSystemConfig, false},
		{RoleUser, PermDomainRead, true},
		{RoleUser, PermDomainCreate, false},
		{RoleUser, PermUserRead, false},
		{"invalid", PermDomainRead, false},
	}

	for _, tt := range tests {
		got := m.HasPermission(tt.role, tt.perm)
		if got != tt.want {
			t.Errorf("HasPermission(%s, %s) = %v, want %v", tt.role, tt.perm, got, tt.want)
		}
	}
}

func TestCanManageDomain(t *testing.T) {
	m := newTestManager(t)

	admin := &User{Role: RoleAdmin}
	reseller := &User{Role: RoleReseller, Domains: []string{"a.com", "b.com"}}
	user := &User{Role: RoleUser}

	if !m.CanManageDomain(admin, "any.com") {
		t.Error("admin should manage any domain")
	}
	if !m.CanManageDomain(reseller, "a.com") {
		t.Error("reseller should manage assigned domain")
	}
	if m.CanManageDomain(reseller, "c.com") {
		t.Error("reseller should NOT manage unassigned domain")
	}
	if m.CanManageDomain(user, "a.com") {
		t.Error("user should NOT manage any domain")
	}
}

func TestDeleteUser(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	err := m.DeleteUser("alice")
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, exists := m.GetUser("alice")
	if exists {
		t.Error("user should not exist after deletion")
	}
}

func TestListUsers(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "pass", RoleAdmin, nil)
	m.CreateUser("bob", "", "pass", RoleUser, nil)

	users := m.ListUsers()
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

func TestUserPersistence(t *testing.T) {
	dir := t.TempDir()

	// Create user and save
	m1 := NewManager(dir, "key")
	m1.CreateUser("alice", "alice@test.com", "secret", RoleAdmin, nil)

	// New manager loads from disk
	m2 := NewManager(dir, "key")
	user, exists := m2.GetUser("alice")
	if !exists {
		t.Fatal("expected user to persist across manager instances")
	}
	if user.Email != "alice@test.com" {
		t.Errorf("expected email alice@test.com, got %s", user.Email)
	}
}

func TestContextUser(t *testing.T) {
	user := &User{Username: "test"}
	ctx := WithUser(context.Background(), user)

	got, ok := UserFromContext(ctx)
	if !ok {
		t.Fatal("expected user from context")
	}
	if got.Username != "test" {
		t.Errorf("expected test, got %s", got.Username)
	}
}

func TestContextUserMissing(t *testing.T) {
	_, ok := UserFromContext(context.Background())
	if ok {
		t.Error("expected no user from empty context")
	}
}
