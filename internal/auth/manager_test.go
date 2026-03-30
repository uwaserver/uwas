package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	return NewManager(t.TempDir(), "test-global-api-key")
}

// ---------------------------------------------------------------------------
// CreateUser
// ---------------------------------------------------------------------------

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
	if !strings.HasPrefix(user.APIKey, "uk_") {
		t.Errorf("expected API key to start with uk_, got %s", user.APIKey)
	}
	if !user.Enabled {
		t.Error("expected user to be enabled")
	}
	if user.ID == "" {
		t.Error("expected user ID to be generated")
	}
	if user.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if user.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
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

func TestCreateUserWithDomains(t *testing.T) {
	m := newTestManager(t)

	domains := []string{"example.com", "test.com"}
	user, err := m.CreateUser("reseller1", "r@test.com", "pass", RoleReseller, domains)
	if err != nil {
		t.Fatalf("CreateUser with domains: %v", err)
	}
	if len(user.Domains) != 2 {
		t.Errorf("expected 2 domains, got %d", len(user.Domains))
	}
	if user.Domains[0] != "example.com" || user.Domains[1] != "test.com" {
		t.Errorf("unexpected domains: %v", user.Domains)
	}
}

func TestCreateUserInvalidUsername(t *testing.T) {
	m := newTestManager(t)

	tests := []struct {
		name     string
		username string
	}{
		{"too short", "ab"},
		{"too long", "abcdefghijklmnopqrstuvwxyz1234567"},
		{"has space", "alice bob"},
		{"has dot", "alice.bob"},
		{"has at", "alice@bob"},
		{"has slash", "alice/bob"},
		{"special chars", "alice!bob"},
		{"unicode", "alice\u00e9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.CreateUser(tt.username, "", "pass", RoleUser, nil)
			if err == nil {
				t.Errorf("expected error for username %q", tt.username)
			}
		})
	}
}

func TestCreateUserValidUsernames(t *testing.T) {
	m := newTestManager(t)

	tests := []string{"abc", "alice", "ALICE", "user_1", "my-user", "abcdefghijklmnopqrstuvwxyz123456"}

	for _, username := range tests {
		t.Run(username, func(t *testing.T) {
			_, err := m.CreateUser(username, "", "pass", RoleUser, nil)
			if err != nil {
				t.Errorf("expected no error for username %q, got %v", username, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Authenticate
// ---------------------------------------------------------------------------

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
	if session.Role != RoleAdmin {
		t.Errorf("expected role admin, got %s", session.Role)
	}
	if session.UserID == "" {
		t.Error("expected user ID in session")
	}
	if session.ExpiresAt.Before(time.Now()) {
		t.Error("expected session expiry to be in the future")
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

func TestAuthenticateDisabledUser(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Disable the user
	m.mu.Lock()
	m.users["alice"].Enabled = false
	m.mu.Unlock()

	_, err := m.Authenticate("alice", "secret")
	if err == nil {
		t.Error("expected error for disabled user")
	}
	if err.Error() != "user disabled" {
		t.Errorf("expected 'user disabled' error, got %q", err.Error())
	}
}

func TestAuthenticateUpdatesLastLogin(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	before := time.Now()
	m.Authenticate("alice", "secret")

	user, _ := m.GetUser("alice")
	if user.LastLogin.Before(before) {
		t.Error("expected LastLogin to be updated after authentication")
	}
}

func TestAuthenticateSessionHasDomains(t *testing.T) {
	m := newTestManager(t)
	domains := []string{"a.com", "b.com"}
	m.CreateUser("reseller1", "", "secret", RoleReseller, domains)

	session, err := m.Authenticate("reseller1", "secret")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if len(session.Domains) != 2 {
		t.Errorf("expected 2 domains in session, got %d", len(session.Domains))
	}
}

// ---------------------------------------------------------------------------
// ValidateSession
// ---------------------------------------------------------------------------

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

func TestValidateSessionExpired(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	session, _ := m.Authenticate("alice", "secret")

	// Manually expire the session
	m.mu.Lock()
	m.sessions[session.Token].ExpiresAt = time.Now().Add(-1 * time.Hour)
	m.mu.Unlock()

	_, err := m.ValidateSession(session.Token)
	if err == nil {
		t.Error("expected error for expired session")
	}
	if err.Error() != "session expired" {
		t.Errorf("expected 'session expired' error, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Logout
// ---------------------------------------------------------------------------

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

func TestLogoutNonexistentToken(t *testing.T) {
	m := newTestManager(t)
	// Should not panic on nonexistent token
	m.Logout("nonexistent-token")
}

// ---------------------------------------------------------------------------
// AuthenticateAPIKey
// ---------------------------------------------------------------------------

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

func TestAuthenticateAPIKeyDisabledUser(t *testing.T) {
	m := newTestManager(t)
	user, _ := m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Disable the user
	m.mu.Lock()
	m.users["alice"].Enabled = false
	m.mu.Unlock()

	_, err := m.AuthenticateAPIKey(user.APIKey)
	if err == nil {
		t.Error("expected error for disabled user API key")
	}
}

func TestAuthenticateAPIKeyEmptyGlobalKey(t *testing.T) {
	// Manager with no global API key
	m := NewManager(t.TempDir(), "")

	user, _ := m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Per-user key should still work
	authenticated, err := m.AuthenticateAPIKey(user.APIKey)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if authenticated.Username != "alice" {
		t.Errorf("expected alice, got %s", authenticated.Username)
	}

	// Empty string should not match empty global key
	_, err = m.AuthenticateAPIKey("")
	if err == nil {
		t.Error("expected error for empty API key against empty global key")
	}
}

// ---------------------------------------------------------------------------
// HasPermission
// ---------------------------------------------------------------------------

func TestHasPermission(t *testing.T) {
	m := newTestManager(t)

	tests := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleAdmin, PermDomainCreate, true},
		{RoleAdmin, PermDomainRead, true},
		{RoleAdmin, PermDomainUpdate, true},
		{RoleAdmin, PermDomainDelete, true},
		{RoleAdmin, PermUserRead, true},
		{RoleAdmin, PermUserCreate, true},
		{RoleAdmin, PermUserUpdate, true},
		{RoleAdmin, PermUserDelete, true},
		{RoleAdmin, PermSystemRead, true},
		{RoleAdmin, PermSystemConfig, true},
		{RoleAdmin, PermBackupManage, true},
		{RoleAdmin, PermCertManage, true},
		{RoleReseller, PermDomainCreate, true},
		{RoleReseller, PermDomainRead, true},
		{RoleReseller, PermDomainUpdate, true},
		{RoleReseller, PermDomainDelete, true},
		{RoleReseller, PermUserRead, true},
		{RoleReseller, PermUserCreate, false},
		{RoleReseller, PermUserUpdate, false},
		{RoleReseller, PermUserDelete, false},
		{RoleReseller, PermSystemRead, true},
		{RoleReseller, PermSystemConfig, false},
		{RoleReseller, PermBackupManage, false},
		{RoleReseller, PermCertManage, true},
		{RoleUser, PermDomainRead, true},
		{RoleUser, PermDomainCreate, false},
		{RoleUser, PermDomainUpdate, false},
		{RoleUser, PermDomainDelete, false},
		{RoleUser, PermUserRead, false},
		{RoleUser, PermSystemRead, true},
		{RoleUser, PermSystemConfig, false},
		{RoleUser, PermBackupManage, false},
		{RoleUser, PermCertManage, false},
		{"invalid", PermDomainRead, false},
	}

	for _, tt := range tests {
		got := m.HasPermission(tt.role, tt.perm)
		if got != tt.want {
			t.Errorf("HasPermission(%s, %s) = %v, want %v", tt.role, tt.perm, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CanManageDomain
// ---------------------------------------------------------------------------

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
	if !m.CanManageDomain(reseller, "b.com") {
		t.Error("reseller should manage second assigned domain")
	}
	if m.CanManageDomain(reseller, "c.com") {
		t.Error("reseller should NOT manage unassigned domain")
	}
	if m.CanManageDomain(user, "a.com") {
		t.Error("user should NOT manage any domain")
	}
}

func TestCanManageDomainResellerNoDomains(t *testing.T) {
	m := newTestManager(t)
	reseller := &User{Role: RoleReseller, Domains: nil}

	if m.CanManageDomain(reseller, "any.com") {
		t.Error("reseller with no domains should NOT manage any domain")
	}
}

// ---------------------------------------------------------------------------
// GetUser / GetUserByID
// ---------------------------------------------------------------------------

func TestGetUserByID(t *testing.T) {
	m := newTestManager(t)
	created, _ := m.CreateUser("alice", "alice@test.com", "secret", RoleAdmin, nil)

	user, exists := m.GetUserByID(created.ID)
	if !exists {
		t.Fatal("expected user to be found by ID")
	}
	if user.Username != "alice" {
		t.Errorf("expected alice, got %s", user.Username)
	}
}

func TestGetUserByIDNotFound(t *testing.T) {
	m := newTestManager(t)

	_, exists := m.GetUserByID("nonexistent-id")
	if exists {
		t.Error("expected no user for nonexistent ID")
	}
}

func TestGetUserNotFound(t *testing.T) {
	m := newTestManager(t)

	_, exists := m.GetUser("nonexistent")
	if exists {
		t.Error("expected no user for nonexistent username")
	}
}

// ---------------------------------------------------------------------------
// DeleteUser
// ---------------------------------------------------------------------------

func TestDeleteUser(t *testing.T) {
	m := newTestManager(t)
	created, _ := m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	err := m.DeleteUser("alice")
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, exists := m.GetUser("alice")
	if exists {
		t.Error("user should not exist after deletion")
	}

	// Also verify usersByID is cleaned up
	_, existsByID := m.GetUserByID(created.ID)
	if existsByID {
		t.Error("user should not exist by ID after deletion")
	}
}

func TestDeleteUserNotFound(t *testing.T) {
	m := newTestManager(t)

	err := m.DeleteUser("nonexistent")
	if err == nil {
		t.Error("expected error for deleting nonexistent user")
	}
	if err.Error() != "user not found" {
		t.Errorf("expected 'user not found' error, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// UpdateUser
// ---------------------------------------------------------------------------

func TestUpdateUserEmail(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "old@test.com", "secret", RoleAdmin, nil)

	err := m.UpdateUser("alice", &User{Email: "new@test.com"})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	user, _ := m.GetUser("alice")
	if user.Email != "new@test.com" {
		t.Errorf("expected new@test.com, got %s", user.Email)
	}
}

func TestUpdateUserPassword(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "oldpass", RoleAdmin, nil)

	// Note: Enabled defaults to false in the updates struct, which will
	// flip the user to disabled if the user was enabled. We must set
	// Enabled: true to preserve the existing state.
	err := m.UpdateUser("alice", &User{Password: "newpass", Enabled: true})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	// Old password should not work
	_, err = m.Authenticate("alice", "oldpass")
	if err == nil {
		t.Error("expected old password to fail after update")
	}

	// New password should work
	_, err = m.Authenticate("alice", "newpass")
	if err != nil {
		t.Errorf("expected new password to work, got %v", err)
	}
}

func TestUpdateUserRole(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleUser, nil)

	err := m.UpdateUser("alice", &User{Role: RoleAdmin})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	user, _ := m.GetUser("alice")
	if user.Role != RoleAdmin {
		t.Errorf("expected admin role, got %s", user.Role)
	}
}

func TestUpdateUserInvalidRole(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleUser, nil)

	// Invalid role should be ignored (isValidRole check)
	err := m.UpdateUser("alice", &User{Role: "superadmin"})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	user, _ := m.GetUser("alice")
	if user.Role != RoleUser {
		t.Errorf("expected role to remain user, got %s", user.Role)
	}
}

func TestUpdateUserDomains(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleReseller, []string{"a.com"})

	err := m.UpdateUser("alice", &User{Domains: []string{"b.com", "c.com"}})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	user, _ := m.GetUser("alice")
	if len(user.Domains) != 2 || user.Domains[0] != "b.com" {
		t.Errorf("expected [b.com c.com], got %v", user.Domains)
	}
}

func TestUpdateUserEnabled(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Disable user
	err := m.UpdateUser("alice", &User{Enabled: false})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	user, _ := m.GetUser("alice")
	if user.Enabled {
		t.Error("expected user to be disabled")
	}
}

func TestUpdateUserNotFound(t *testing.T) {
	m := newTestManager(t)

	err := m.UpdateUser("nonexistent", &User{Email: "test@test.com"})
	if err == nil {
		t.Error("expected error for nonexistent user")
	}
	if err.Error() != "user not found" {
		t.Errorf("expected 'user not found' error, got %q", err.Error())
	}
}

func TestUpdateUserUpdatesTimestamp(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	user, _ := m.GetUser("alice")
	originalUpdate := user.UpdatedAt

	// Sleep briefly to ensure time difference
	time.Sleep(10 * time.Millisecond)

	m.UpdateUser("alice", &User{Email: "new@test.com"})
	user, _ = m.GetUser("alice")
	if !user.UpdatedAt.After(originalUpdate) {
		t.Error("expected UpdatedAt to be updated")
	}
}

func TestUpdateUserMultipleFields(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "old@test.com", "secret", RoleUser, nil)

	err := m.UpdateUser("alice", &User{
		Email:   "new@test.com",
		Role:    RoleReseller,
		Domains: []string{"x.com"},
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	user, _ := m.GetUser("alice")
	if user.Email != "new@test.com" {
		t.Errorf("expected new@test.com, got %s", user.Email)
	}
	if user.Role != RoleReseller {
		t.Errorf("expected reseller role, got %s", user.Role)
	}
	if len(user.Domains) != 1 || user.Domains[0] != "x.com" {
		t.Errorf("expected [x.com], got %v", user.Domains)
	}
}

func TestUpdateUserEmptyFieldsNoChange(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "alice@test.com", "secret", RoleAdmin, nil)

	// Update with empty fields - should not change email, password, role
	err := m.UpdateUser("alice", &User{})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	user, _ := m.GetUser("alice")
	if user.Email != "alice@test.com" {
		t.Errorf("email should not change, got %s", user.Email)
	}
	if user.Role != RoleAdmin {
		t.Errorf("role should not change, got %s", user.Role)
	}
}

// ---------------------------------------------------------------------------
// RegenerateAPIKey
// ---------------------------------------------------------------------------

func TestRegenerateAPIKey(t *testing.T) {
	m := newTestManager(t)
	user, _ := m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	oldKey := user.APIKey

	newKey, err := m.RegenerateAPIKey("alice")
	if err != nil {
		t.Fatalf("RegenerateAPIKey: %v", err)
	}
	if newKey == "" {
		t.Error("expected new API key")
	}
	if newKey == oldKey {
		t.Error("expected new API key to differ from old")
	}
	if !strings.HasPrefix(newKey, "uk_") {
		t.Errorf("expected API key to start with uk_, got %s", newKey)
	}

	// Old key should no longer work
	_, err = m.AuthenticateAPIKey(oldKey)
	if err == nil {
		t.Error("expected old API key to be invalid")
	}

	// New key should work
	authenticated, err := m.AuthenticateAPIKey(newKey)
	if err != nil {
		t.Fatalf("new API key should work: %v", err)
	}
	if authenticated.Username != "alice" {
		t.Errorf("expected alice, got %s", authenticated.Username)
	}
}

func TestRegenerateAPIKeyNotFound(t *testing.T) {
	m := newTestManager(t)

	_, err := m.RegenerateAPIKey("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent user")
	}
	if err.Error() != "user not found" {
		t.Errorf("expected 'user not found' error, got %q", err.Error())
	}
}

func TestRegenerateAPIKeyUpdatesTimestamp(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	user, _ := m.GetUser("alice")
	originalUpdate := user.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	m.RegenerateAPIKey("alice")
	user, _ = m.GetUser("alice")
	if !user.UpdatedAt.After(originalUpdate) {
		t.Error("expected UpdatedAt to be updated after key regeneration")
	}
}

// ---------------------------------------------------------------------------
// ChangePassword
// ---------------------------------------------------------------------------

func TestChangePassword(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "oldpass", RoleAdmin, nil)

	err := m.ChangePassword("alice", "oldpass", "newpass")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// Old password should not work
	_, err = m.Authenticate("alice", "oldpass")
	if err == nil {
		t.Error("expected old password to fail")
	}

	// New password should work
	_, err = m.Authenticate("alice", "newpass")
	if err != nil {
		t.Errorf("expected new password to work, got %v", err)
	}
}

func TestChangePasswordWrongCurrent(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	err := m.ChangePassword("alice", "wrongcurrent", "newpass")
	if err == nil {
		t.Error("expected error for wrong current password")
	}
	if err.Error() != "invalid current password" {
		t.Errorf("expected 'invalid current password' error, got %q", err.Error())
	}

	// Original password should still work
	_, err = m.Authenticate("alice", "secret")
	if err != nil {
		t.Errorf("original password should still work: %v", err)
	}
}

func TestChangePasswordNotFound(t *testing.T) {
	m := newTestManager(t)

	err := m.ChangePassword("nonexistent", "old", "new")
	if err == nil {
		t.Error("expected error for nonexistent user")
	}
	if err.Error() != "user not found" {
		t.Errorf("expected 'user not found' error, got %q", err.Error())
	}
}

func TestChangePasswordUpdatesTimestamp(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "oldpass", RoleAdmin, nil)
	user, _ := m.GetUser("alice")
	originalUpdate := user.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	m.ChangePassword("alice", "oldpass", "newpass")
	user, _ = m.GetUser("alice")
	if !user.UpdatedAt.After(originalUpdate) {
		t.Error("expected UpdatedAt to be updated after password change")
	}
}

// ---------------------------------------------------------------------------
// CleanupSessions
// ---------------------------------------------------------------------------

func TestCleanupSessions(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Create two sessions
	s1, _ := m.Authenticate("alice", "secret")
	s2, _ := m.Authenticate("alice", "secret")

	// Expire only s1
	m.mu.Lock()
	m.sessions[s1.Token].ExpiresAt = time.Now().Add(-1 * time.Hour)
	m.mu.Unlock()

	m.CleanupSessions()

	// s1 should be gone
	_, err := m.ValidateSession(s1.Token)
	if err == nil {
		t.Error("expected expired session to be cleaned up")
	}

	// s2 should still be valid
	_, err = m.ValidateSession(s2.Token)
	if err != nil {
		t.Errorf("expected valid session to survive cleanup: %v", err)
	}
}

func TestCleanupSessionsEmpty(t *testing.T) {
	m := newTestManager(t)
	// Should not panic with no sessions
	m.CleanupSessions()
}

func TestCleanupSessionsAllExpired(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	s1, _ := m.Authenticate("alice", "secret")
	s2, _ := m.Authenticate("alice", "secret")

	// Expire both
	m.mu.Lock()
	m.sessions[s1.Token].ExpiresAt = time.Now().Add(-1 * time.Hour)
	m.sessions[s2.Token].ExpiresAt = time.Now().Add(-2 * time.Hour)
	m.mu.Unlock()

	m.CleanupSessions()

	m.mu.RLock()
	count := len(m.sessions)
	m.mu.RUnlock()

	if count != 0 {
		t.Errorf("expected 0 sessions after cleanup, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// ListUsers
// ---------------------------------------------------------------------------

func TestListUsers(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "pass", RoleAdmin, nil)
	m.CreateUser("bob", "", "pass", RoleUser, nil)

	users := m.ListUsers()
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

func TestListUsersEmpty(t *testing.T) {
	m := newTestManager(t)
	users := m.ListUsers()
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}

// ---------------------------------------------------------------------------
// Persistence (save/load)
// ---------------------------------------------------------------------------

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

func TestPersistenceMultipleUsers(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "key")
	m1.CreateUser("alice", "alice@test.com", "pass", RoleAdmin, nil)
	m1.CreateUser("bob", "bob@test.com", "pass", RoleReseller, []string{"b.com"})
	m1.CreateUser("carol", "", "pass", RoleUser, nil)

	m2 := NewManager(dir, "key")
	users := m2.ListUsers()
	if len(users) != 3 {
		t.Errorf("expected 3 persisted users, got %d", len(users))
	}

	bob, exists := m2.GetUser("bob")
	if !exists {
		t.Fatal("expected bob to persist")
	}
	if bob.Role != RoleReseller {
		t.Errorf("expected reseller role for bob, got %s", bob.Role)
	}
	if len(bob.Domains) != 1 || bob.Domains[0] != "b.com" {
		t.Errorf("expected [b.com] domains for bob, got %v", bob.Domains)
	}
}

func TestPersistenceByID(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "key")
	created, _ := m1.CreateUser("alice", "", "pass", RoleAdmin, nil)
	userID := created.ID

	m2 := NewManager(dir, "key")
	user, exists := m2.GetUserByID(userID)
	if !exists {
		t.Fatal("expected user to be found by ID after reload")
	}
	if user.Username != "alice" {
		t.Errorf("expected alice, got %s", user.Username)
	}
}

func TestPersistenceAfterDelete(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "key")
	m1.CreateUser("alice", "", "pass", RoleAdmin, nil)
	m1.CreateUser("bob", "", "pass", RoleUser, nil)
	m1.DeleteUser("alice")

	m2 := NewManager(dir, "key")
	_, exists := m2.GetUser("alice")
	if exists {
		t.Error("deleted user should not persist")
	}
	_, exists = m2.GetUser("bob")
	if !exists {
		t.Error("remaining user should persist")
	}
}

func TestPersistenceAfterUpdate(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "key")
	m1.CreateUser("alice", "old@test.com", "pass", RoleUser, nil)
	m1.UpdateUser("alice", &User{Email: "new@test.com", Role: RoleAdmin})

	m2 := NewManager(dir, "key")
	user, _ := m2.GetUser("alice")
	if user.Email != "new@test.com" {
		t.Errorf("expected updated email to persist, got %s", user.Email)
	}
	if user.Role != RoleAdmin {
		t.Errorf("expected updated role to persist, got %s", user.Role)
	}
}

func TestLoadUsersInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	// Write invalid JSON to users file
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "users.json"), []byte("not valid json{{{"), 0600)

	// Should not panic, just start with empty users
	m := NewManager(dir, "key")
	users := m.ListUsers()
	if len(users) != 0 {
		t.Errorf("expected 0 users after invalid JSON load, got %d", len(users))
	}
}

func TestLoadUsersNonexistentFile(t *testing.T) {
	dir := t.TempDir()
	// No users.json file exists - should gracefully handle
	m := NewManager(dir, "key")
	users := m.ListUsers()
	if len(users) != 0 {
		t.Errorf("expected 0 users when no file exists, got %d", len(users))
	}
}

func TestNewManagerEmptyDataDir(t *testing.T) {
	// Empty dataDir - persistence disabled
	m := NewManager("", "key")
	user, err := m.CreateUser("alice", "", "pass", RoleAdmin, nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.Username != "alice" {
		t.Errorf("expected alice, got %s", user.Username)
	}
	// usersFile returns "" so save/load are no-ops
}

func TestUsersFileEmptyDataDir(t *testing.T) {
	m := &Manager{dataDir: ""}
	if m.usersFile() != "" {
		t.Error("expected empty string for usersFile with empty dataDir")
	}
}

func TestUsersFileWithDataDir(t *testing.T) {
	m := &Manager{dataDir: "/some/path"}
	expected := filepath.Join("/some/path", "users.json")
	if m.usersFile() != expected {
		t.Errorf("expected %s, got %s", expected, m.usersFile())
	}
}

// ---------------------------------------------------------------------------
// Context
// ---------------------------------------------------------------------------

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

func TestContextUserWrongType(t *testing.T) {
	// Put a non-User value with the same key type
	ctx := context.WithValue(context.Background(), userContextKey, "not-a-user")
	_, ok := UserFromContext(ctx)
	if ok {
		t.Error("expected false for non-User value in context")
	}
}

// ---------------------------------------------------------------------------
// isPublicEndpoint
// ---------------------------------------------------------------------------

func TestIsPublicEndpoint(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/health", true},
		{"/api/v1/health/check", true},
		{"/api/v1/healthcheck", false},
		{"/api/v1/auth/login", true},
		{"/api/v1/auth/login/", true},
		{"/api/v1/auth/loginx", false},
		{"/_uwas/dashboard", true},
		{"/_uwas/dashboard/settings", true},
		{"/_uwas/dashboardx", false},
		{"/api/v1/domains", false},
		{"/api/v1/users", false},
		{"/api/v1/auth/logout", false},
		{"/", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isPublicEndpoint(tt.path)
			if got != tt.want {
				t.Errorf("isPublicEndpoint(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isValidRole
// ---------------------------------------------------------------------------

func TestIsValidRole(t *testing.T) {
	tests := []struct {
		role Role
		want bool
	}{
		{RoleAdmin, true},
		{RoleReseller, true},
		{RoleUser, true},
		{"superadmin", false},
		{"", false},
		{"ADMIN", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := isValidRole(tt.role)
			if got != tt.want {
				t.Errorf("isValidRole(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isValidUsername
// ---------------------------------------------------------------------------

func TestIsValidUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{"valid lowercase", "alice", true},
		{"valid uppercase", "ALICE", true},
		{"valid mixed", "Alice123", true},
		{"valid underscore", "alice_bob", true},
		{"valid hyphen", "alice-bob", true},
		{"min length", "abc", true},
		{"max length", "abcdefghijklmnopqrstuvwxyz123456", true},
		{"too short", "ab", false},
		{"too long", "abcdefghijklmnopqrstuvwxyz1234567", false},
		{"has space", "alice bob", false},
		{"has dot", "alice.bob", false},
		{"has at sign", "alice@bob", false},
		{"empty", "", false},
		{"single char", "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidUsername(tt.username)
			if got != tt.want {
				t.Errorf("isValidUsername(%q) = %v, want %v", tt.username, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func TestMiddlewarePublicEndpoint(t *testing.T) {
	m := newTestManager(t)

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	// Public endpoints should pass through without auth
	paths := []string{"/api/v1/health", "/api/v1/auth/login", "/_uwas/dashboard"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected 200 for public endpoint %s, got %d", path, rr.Code)
			}
		})
	}
}

func TestMiddlewareUnauthorized(t *testing.T) {
	m := newTestManager(t)

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Errorf("expected unauthorized error body, got %q", rr.Body.String())
	}
}

func TestMiddlewareAPIKeyAuth(t *testing.T) {
	m := newTestManager(t)
	user, _ := m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	var ctxUser *User
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if ok {
			ctxUser = u
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer "+user.APIKey)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if ctxUser == nil {
		t.Fatal("expected user in context")
	}
	if ctxUser.Username != "alice" {
		t.Errorf("expected alice in context, got %s", ctxUser.Username)
	}
}

func TestMiddlewareGlobalAPIKeyAuth(t *testing.T) {
	m := newTestManager(t)

	var ctxUser *User
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if ok {
			ctxUser = u
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer test-global-api-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if ctxUser == nil {
		t.Fatal("expected user in context")
	}
	if ctxUser.Role != RoleAdmin {
		t.Errorf("expected admin role, got %s", ctxUser.Role)
	}
}

func TestMiddlewareSessionAuth(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	session, _ := m.Authenticate("alice", "secret")

	var ctxUser *User
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if ok {
			ctxUser = u
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("X-Session-Token", session.Token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if ctxUser == nil {
		t.Fatal("expected user in context")
	}
	if ctxUser.Username != "alice" {
		t.Errorf("expected alice in context, got %s", ctxUser.Username)
	}
}

func TestMiddlewareInvalidAPIKey(t *testing.T) {
	m := newTestManager(t)

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer invalid-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMiddlewareInvalidSessionToken(t *testing.T) {
	m := newTestManager(t)

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("X-Session-Token", "invalid-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMiddlewareExpiredSession(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	session, _ := m.Authenticate("alice", "secret")

	// Expire the session
	m.mu.Lock()
	m.sessions[session.Token].ExpiresAt = time.Now().Add(-1 * time.Hour)
	m.mu.Unlock()

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("X-Session-Token", session.Token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMiddlewareAPIKeyPrecedence(t *testing.T) {
	m := newTestManager(t)
	user, _ := m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	m.CreateUser("bob", "", "secret", RoleUser, nil)
	sessionBob, _ := m.Authenticate("bob", "secret")

	var ctxUser *User
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if ok {
			ctxUser = u
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Both API key (alice) and session token (bob) present - API key should win
	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer "+user.APIKey)
	req.Header.Set("X-Session-Token", sessionBob.Token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if ctxUser == nil {
		t.Fatal("expected user in context")
	}
	if ctxUser.Username != "alice" {
		t.Errorf("expected alice (API key user) to take precedence, got %s", ctxUser.Username)
	}
}

func TestMiddlewareSessionWithDeletedUser(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	session, _ := m.Authenticate("alice", "secret")

	// Delete the user - session still exists but user doesn't
	m.DeleteUser("alice")

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("X-Session-Token", session.Token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Session is valid but user lookup fails -> unauthorized
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for deleted user session, got %d", rr.Code)
	}
}

func TestMiddlewareNonBearerAuthHeader(t *testing.T) {
	m := newTestManager(t)

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Authorization header that doesn't start with "Bearer "
	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for non-Bearer auth, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentCreateAndRead(t *testing.T) {
	m := newTestManager(t)

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// Concurrently create 10 users
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			username := strings.ReplaceAll(strings.ReplaceAll(
				strings.ReplaceAll("user-NNN", "NNN",
					strings.Repeat("0", 3-len(itoa(i)))+itoa(i)),
				" ", ""), "\t", "")
			_, err := m.CreateUser(username, "", "pass", RoleUser, nil)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	// Concurrently read users
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.ListUsers()
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}

	users := m.ListUsers()
	if len(users) != 10 {
		t.Errorf("expected 10 users, got %d", len(users))
	}
}

func TestConcurrentAuthAndValidate(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	var wg sync.WaitGroup

	// Concurrently authenticate and validate
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := m.Authenticate("alice", "secret")
			if err != nil {
				return
			}
			m.ValidateSession(session.Token)
			m.Logout(session.Token)
		}()
	}

	wg.Wait()
}

func TestConcurrentCleanup(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Create several sessions
	for i := 0; i < 5; i++ {
		m.Authenticate("alice", "secret")
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.CleanupSessions()
		}()
	}
	wg.Wait()
}

// itoa is a simple int-to-string helper for test use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestGenerateIDUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateID()
		if id == "" {
			t.Fatal("generateID returned empty string")
		}
		if seen[id] {
			t.Fatalf("generateID produced duplicate: %s", id)
		}
		seen[id] = true
	}
}

func TestGenerateTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token := generateToken()
		if token == "" {
			t.Fatal("generateToken returned empty string")
		}
		if seen[token] {
			t.Fatalf("generateToken produced duplicate: %s", token)
		}
		seen[token] = true
	}
}

func TestGenerateAPIKeyFormat(t *testing.T) {
	for i := 0; i < 10; i++ {
		key := generateAPIKey()
		if !strings.HasPrefix(key, "uk_") {
			t.Errorf("expected API key to start with uk_, got %s", key)
		}
		if len(key) < 10 {
			t.Errorf("expected API key to be longer, got %s", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Role permissions completeness
// ---------------------------------------------------------------------------

func TestAllRolesHavePermissions(t *testing.T) {
	for _, role := range []Role{RoleAdmin, RoleReseller, RoleUser} {
		perms, exists := rolePermissions[role]
		if !exists {
			t.Errorf("role %s has no permissions defined", role)
		}
		if len(perms) == 0 {
			t.Errorf("role %s has empty permissions", role)
		}
	}
}

func TestAdminHasAllPermissions(t *testing.T) {
	allPerms := []Permission{
		PermDomainRead, PermDomainCreate, PermDomainUpdate, PermDomainDelete,
		PermUserRead, PermUserCreate, PermUserUpdate, PermUserDelete,
		PermSystemRead, PermSystemConfig,
		PermBackupManage, PermCertManage,
	}

	m := newTestManager(t)
	for _, perm := range allPerms {
		if !m.HasPermission(RoleAdmin, perm) {
			t.Errorf("admin should have permission %s", perm)
		}
	}
}

// ---------------------------------------------------------------------------
// Bcrypt error paths (password > 72 bytes)
// ---------------------------------------------------------------------------

func TestCreateUserPasswordTooLong(t *testing.T) {
	m := newTestManager(t)
	longPass := strings.Repeat("a", 73)

	_, err := m.CreateUser("alice", "", longPass, RoleAdmin, nil)
	if err == nil {
		t.Error("expected error for password exceeding 72 bytes")
	}
	if !strings.Contains(err.Error(), "hash password") {
		t.Errorf("expected hash password error, got %q", err.Error())
	}
}

func TestUpdateUserPasswordTooLong(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	longPass := strings.Repeat("a", 73)

	err := m.UpdateUser("alice", &User{Password: longPass, Enabled: true})
	if err == nil {
		t.Error("expected error for password exceeding 72 bytes in update")
	}
	if !strings.Contains(err.Error(), "hash password") {
		t.Errorf("expected hash password error, got %q", err.Error())
	}
}

func TestChangePasswordNewTooLong(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	longPass := strings.Repeat("a", 73)

	err := m.ChangePassword("alice", "secret", longPass)
	if err == nil {
		t.Error("expected error for new password exceeding 72 bytes")
	}
	if !strings.Contains(err.Error(), "hash password") {
		t.Errorf("expected hash password error, got %q", err.Error())
	}

	// Original password should still work
	_, err = m.Authenticate("alice", "secret")
	if err != nil {
		t.Errorf("original password should still work after failed change: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestAuthenticateAPIKeyWithMultipleUsers(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)
	bob, _ := m.CreateUser("bob", "", "secret", RoleUser, nil)
	m.CreateUser("carol", "", "secret", RoleReseller, nil)

	// Authenticate bob's key specifically
	authenticated, err := m.AuthenticateAPIKey(bob.APIKey)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if authenticated.Username != "bob" {
		t.Errorf("expected bob, got %s", authenticated.Username)
	}
}

func TestPersistenceAfterRegenerateAPIKey(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "key")
	m1.CreateUser("alice", "", "pass", RoleAdmin, nil)
	newKey, _ := m1.RegenerateAPIKey("alice")

	m2 := NewManager(dir, "key")
	user, _ := m2.GetUser("alice")
	if user.APIKey != newKey {
		t.Errorf("expected regenerated key to persist, got %s", user.APIKey)
	}
}

func TestPersistenceAfterChangePassword(t *testing.T) {
	dir := t.TempDir()

	m1 := NewManager(dir, "key")
	m1.CreateUser("alice", "", "oldpass", RoleAdmin, nil)
	m1.ChangePassword("alice", "oldpass", "newpass")

	m2 := NewManager(dir, "key")
	// Old password should not work
	_, err := m2.Authenticate("alice", "oldpass")
	if err == nil {
		t.Error("expected old password to fail after persistence")
	}
	// New password should work
	_, err = m2.Authenticate("alice", "newpass")
	if err != nil {
		t.Errorf("expected new password to work after persistence: %v", err)
	}
}
