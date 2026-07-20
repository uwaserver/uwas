package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

// =============================================================================
// isAdmin coverage — 0% → 100%.
// The isAdmin function at handlers_auth.go:149 reports whether the
// authenticated user has the admin role. Single-key mode (no authMgr)
// treats every caller as admin.
// =============================================================================

func TestAuthIsAdmin_NoAuthMgr(t *testing.T) {
	s := testServer() // authMgr is nil
	if !s.isAdmin(httptest.NewRequest("GET", "/x", nil)) {
		t.Error("isAdmin should return true when authMgr is nil (single-key mode)")
	}
}

func TestAuthIsAdmin_NoUserContext(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	if s.isAdmin(httptest.NewRequest("GET", "/x", nil)) {
		t.Error("isAdmin should return false when no user in context")
	}
}

func TestAuthIsAdmin_AdminUser(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	if !s.isAdmin(withAdminContext(httptest.NewRequest("GET", "/x", nil))) {
		t.Error("isAdmin should return true for admin role")
	}
}

func TestAuthIsAdmin_ResellerUser(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	if s.isAdmin(withResellerContext(httptest.NewRequest("GET", "/x", nil))) {
		t.Error("isAdmin should return false for reseller role")
	}
}

func TestAuthIsAdmin_RegularUser(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	// withUserContext is defined in admin_coverage2_test.go
	if s.isAdmin(withUserContext(httptest.NewRequest("GET", "/x", nil))) {
		t.Error("isAdmin should return false for regular user role")
	}
}

// =============================================================================
// handleUserCreate (SFTP user) — currently 38.5%.
// Existing tests cover requireAdmin, invalid JSON, missing domain.
// New tests cover the remaining branches: siteUserRoot error, empty root,
// siteuser create error.
// Route: POST /api/v1/users
// =============================================================================

func TestAuthSFTPUserCreate_SiteUserRootError(t *testing.T) {
	// Pass a domain that causes siteUserRoot to return an error.
	// Domain "nonexistent" is not in the test config.
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"domain":"nonexistent"}`)
	s.handleUserCreate(rec, withAdminContext(httptest.NewRequest("POST", "/x", body)))
	// siteUserRoot may either error (400) or fall back (→ 500 from
	// CreateUserForWebDir). Accept either since the filesystem is real.
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 400 or 500, body: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthSFTPUserCreate_SystemError(t *testing.T) {
	// Use a configured domain, which gives siteUserRoot a valid path,
	// then CreateUserForWebDir tries to create a real system user and
	// should fail under the test environment.
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"domain":"example.com"}`)
	s.handleUserCreate(rec, withAdminContext(httptest.NewRequest("POST", "/x", body)))
	// siteUserRoot succeeds for "example.com" (in test config),
	// then CreateUserForWebDir fails because it can't create real users.
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, body: %s", rec.Code, rec.Body.String())
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &bodyMap); err == nil {
		if msg, ok := bodyMap["error"]; ok {
			if !strings.Contains(msg.(string), "create user:") {
				t.Errorf("unexpected error message: %v", msg)
			}
		}
	}
}

func TestAuthSFTPUserCreate_MissingDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserCreate(rec, withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{}"))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// handleUserChangePasswordAuth — currently 68.8%.
// Existing tests cover: no authMgr, unauthorized, bad JSON,
// admin success, reseller-to-other (partial).
// New tests cover password policy, reseller self-change with/without
// current_password, and non-admin changing another user's password.
// Route: POST /api/v1/auth/users/{username}/password
// =============================================================================

func TestAuthChangePassword_ShortPassword(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"new_password":"short"}`))
	req.SetPathValue("username", "reseller")
	s.handleUserChangePasswordAuth(rec, withAdminContext(req))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil {
		t.Error("expected error message for short password")
	}
}

func TestAuthChangePassword_ResellerOwn_MissingCurrent(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"new_password":"S3cure-Passw0rd!"}`))
	req.SetPathValue("username", "reseller")
	s.handleUserChangePasswordAuth(rec, withResellerContext(req))
	// Reseller changing own password: must provide current_password → 400
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil {
		t.Error("expected error about current_password")
	}
}

func TestAuthChangePassword_ResellerOwn_Valid(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"password","new_password":"S3cure-Passw0rd!"}`))
	req.SetPathValue("username", "reseller")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserChangePasswordAuth(rec, withResellerContext(req))
	// Reseller with correct current_password and matching username → 200
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "password_changed" {
		t.Errorf("status = %v, want password_changed", body["status"])
	}
}

func TestAuthChangePassword_ResellerOwn_WrongCurrent(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"wrong","new_password":"S3cure-Passw0rd!"}`))
	req.SetPathValue("username", "reseller")
	s.handleUserChangePasswordAuth(rec, withResellerContext(req))
	// Reseller with wrong current_password → 401 from ChangePassword
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401, body: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthChangePassword_RegularUser_ChangeOther(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	// Regular user "user" tries to change admin's password
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"x","new_password":"S3cure-Passw0rd!"}`))
	req.SetPathValue("username", "admin")
	s.handleUserChangePasswordAuth(rec, withUserContext(req))
	// Non-admin, different username → 403
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// handleAuthBootstrap — currently 68.3%.
// Existing tests cover: not enabled, apiKey set, already complete,
// missing input, and success.
// New tests cover: invalid JSON, short password, and
// post-creation authentication failure.
// Route: POST /api/v1/auth/bootstrap
// =============================================================================

func TestAuthBootstrap_InvalidJSON(t *testing.T) {
	s := testServer()
	s.config.Global.Users.Enabled = true
	s.authMgr = &mockAuthManager{users: map[string]*auth.User{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/bootstrap", strings.NewReader(`{bad json}`))
	s.handleAuthBootstrap(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthBootstrap_ShortPassword(t *testing.T) {
	s := testServer()
	empty := newMockAuthManager()
	empty.users = map[string]*auth.User{}
	s.authMgr = empty
	s.config.Global.Users.Enabled = true
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
		strings.NewReader(`{"username":"admin","email":"a@b.com","password":"short"}`))
	s.handleAuthBootstrap(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil {
		t.Error("expected error about password length")
	}
	if !strings.Contains(rec.Body.String(), "password must be at least") {
		t.Errorf("expected password policy error, got: %s", rec.Body.String())
	}
}

func TestAuthBootstrap_AuthenticateFailure(t *testing.T) {
	// After CreateFirstAdmin succeeds, Authenticate is called. The mock's
	// Authenticate only accepts "password" or "S3cure-Passw0rd!", so a
	// policy-compliant but unexpected password triggers a 500.
	s := testServer()
	empty := newMockAuthManager()
	empty.users = map[string]*auth.User{}
	s.authMgr = empty
	s.config.Global.Users.Enabled = true
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
		strings.NewReader(`{"username":"admin","email":"admin@test.com","password":"Valid-but-Unmatched"}`))
	s.handleAuthBootstrap(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, body: %s", rec.Code, rec.Body.String())
	}
}
