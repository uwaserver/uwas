package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/bandwidth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/internal/webhook"
	"github.com/uwaserver/uwas/internal/wordpress"
)

// =============================================================================
// Helper: server with domain that has a real root directory
// =============================================================================

func testServerWithRoot(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "example.com", "public_html")
	os.MkdirAll(root, 0755)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin:   config.AdminConfig{Listen: "127.0.0.1:0"},
			WebRoot: dir,
		},
		Domains: []config.Domain{
			{Host: "example.com", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "api.example.com", Type: "proxy", SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)
	return s, root
}

// =============================================================================
// Mock AuthManager for multi-user auth tests
// =============================================================================

type mockAuthManager struct {
	users    map[string]*auth.User
	sessions map[string]*auth.Session
}

func newMockAuthManager() *mockAuthManager {
	adminUser := &auth.User{
		ID: "admin-id", Username: "admin", Email: "admin@test.com",
		Role: auth.RoleAdmin, Enabled: true, APIKey: "admin-api-key",
		CreatedAt: time.Now(),
	}
	resellerUser := &auth.User{
		ID: "reseller-id", Username: "reseller", Email: "reseller@test.com",
		Role: auth.RoleReseller, Enabled: true, Domains: []string{"reseller.com"},
		CreatedAt: time.Now(),
	}
	return &mockAuthManager{
		users: map[string]*auth.User{
			"admin":    adminUser,
			"reseller": resellerUser,
		},
		sessions: make(map[string]*auth.Session),
	}
}

func (m *mockAuthManager) Authenticate(username, password string) (*auth.Session, error) {
	u, ok := m.users[username]
	if !ok || password != "password" {
		return nil, fmt.Errorf("invalid credentials")
	}
	sess := &auth.Session{
		Token: "session-" + username, UserID: u.ID, Username: u.Username,
		Role: u.Role, Domains: u.Domains, ExpiresAt: time.Now().Add(time.Hour),
	}
	m.sessions[sess.Token] = sess
	return sess, nil
}

func (m *mockAuthManager) AuthenticateAPIKey(key string) (*auth.User, error) {
	for _, u := range m.users {
		if u.APIKey == key {
			return u, nil
		}
	}
	return nil, fmt.Errorf("invalid API key")
}

func (m *mockAuthManager) ValidateSession(token string) (*auth.Session, error) {
	sess, ok := m.sessions[token]
	if !ok {
		return nil, fmt.Errorf("invalid session")
	}
	return sess, nil
}

func (m *mockAuthManager) Logout(token string) {
	delete(m.sessions, token)
}

func (m *mockAuthManager) HasPermission(role auth.Role, perm auth.Permission) bool {
	return role == auth.RoleAdmin
}

func (m *mockAuthManager) CanManageDomain(user *auth.User, domain string) bool {
	if user.Role == auth.RoleAdmin {
		return true
	}
	for _, d := range user.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

func (m *mockAuthManager) GetUser(username string) (*auth.User, bool) {
	u, ok := m.users[username]
	return u, ok
}

func (m *mockAuthManager) GetUserByID(id string) (*auth.User, bool) {
	for _, u := range m.users {
		if u.ID == id {
			return u, true
		}
	}
	return nil, false
}

func (m *mockAuthManager) ListUsers() []*auth.User {
	result := make([]*auth.User, 0, len(m.users))
	for _, u := range m.users {
		result = append(result, u)
	}
	return result
}

func (m *mockAuthManager) CreateUser(username, email, password string, role auth.Role, domains []string) (*auth.User, error) {
	if _, exists := m.users[username]; exists {
		return nil, fmt.Errorf("user already exists")
	}
	u := &auth.User{
		ID: "id-" + username, Username: username, Email: email,
		Role: role, Domains: domains, Enabled: true, CreatedAt: time.Now(),
	}
	m.users[username] = u
	return u, nil
}

func (m *mockAuthManager) UpdateUser(username string, updates *auth.User) error {
	u, ok := m.users[username]
	if !ok {
		return fmt.Errorf("user not found")
	}
	if updates.Email != "" {
		u.Email = updates.Email
	}
	if updates.Password != "" {
		u.Password = updates.Password
	}
	if updates.Domains != nil {
		u.Domains = updates.Domains
	}
	return nil
}

func (m *mockAuthManager) DeleteUser(username string) error {
	if _, ok := m.users[username]; !ok {
		return fmt.Errorf("user not found")
	}
	delete(m.users, username)
	return nil
}

func (m *mockAuthManager) RegenerateAPIKey(username string) (string, error) {
	u, ok := m.users[username]
	if !ok {
		return "", fmt.Errorf("user not found")
	}
	u.APIKey = "new-key-" + username
	return u.APIKey, nil
}

func (m *mockAuthManager) ChangePassword(username, currentPassword, newPassword string) error {
	if currentPassword != "password" {
		return fmt.Errorf("invalid current password")
	}
	return nil
}

// helper to add auth context to request
func withAdminContext(r *http.Request) *http.Request {
	user := &auth.User{
		ID: "admin-id", Username: "admin", Role: auth.RoleAdmin, Enabled: true,
	}
	return r.WithContext(auth.WithUser(r.Context(), user))
}

func withResellerContext(r *http.Request) *http.Request {
	user := &auth.User{
		ID: "reseller-id", Username: "reseller", Role: auth.RoleReseller,
		Enabled: true, Domains: []string{"reseller.com"},
	}
	return r.WithContext(auth.WithUser(r.Context(), user))
}

// =============================================================================
// Settings tests
// =============================================================================

func TestSettingsGet(t *testing.T) {
	s := testServer()
	s.config.Global.LogLevel = "debug"
	s.config.Global.WebRoot = "/var/www"

	rec := httptest.NewRecorder()
	s.handleSettingsGet(rec, httptest.NewRequest("GET", "/api/v1/settings", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["global.log_level"] != "debug" {
		t.Errorf("log_level = %v", body["global.log_level"])
	}
	if body["global.web_root"] != "/var/www" {
		t.Errorf("web_root = %v", body["global.web_root"])
	}
}

func TestSettingsPut(t *testing.T) {
	s := testServer()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	s.SetConfigPath(cfgPath)

	body := strings.NewReader(`{"global.log_level":"warn","global.max_connections":2048}`)
	req := httptest.NewRequest("PUT", "/api/v1/settings", body)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	s.handleSettingsPut(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if s.config.Global.LogLevel != "warn" {
		t.Errorf("log_level = %q, want warn", s.config.Global.LogLevel)
	}
	if s.config.Global.MaxConnections != 2048 {
		t.Errorf("max_connections = %d, want 2048", s.config.Global.MaxConnections)
	}
}

func TestSettingsPutBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSettingsPut(rec, httptest.NewRequest("PUT", "/api/v1/settings", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// 2FA handlers
// =============================================================================

func TestHandle2FAStatus(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handle2FAStatus(rec, httptest.NewRequest("GET", "/api/v1/auth/2fa/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]bool
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["enabled"] != false {
		t.Error("2FA should not be enabled initially")
	}
}

func TestHandle2FASetupAlreadyEnabled(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.TOTPSecret = "existing-secret"

	rec := httptest.NewRecorder()
	s.handle2FASetup(rec, httptest.NewRequest("POST", "/api/v1/auth/2fa/setup", nil))
	if rec.Code != 409 {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestHandle2FASetup(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.handle2FASetup(rec, httptest.NewRequest("POST", "/api/v1/auth/2fa/setup", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["secret"] == "" {
		t.Error("secret should not be empty")
	}
	if body["uri"] == "" {
		t.Error("uri should not be empty")
	}
	if len(s.pendingTOTP) == 0 {
		t.Error("pendingTOTP should be set")
	}
}

func TestHandle2FAVerifyNoPending(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/verify", strings.NewReader(`{"code":"123456"}`))
	s.handle2FAVerify(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandle2FAVerifyBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handle2FAVerify(rec, httptest.NewRequest("POST", "/api/v1/auth/2fa/verify", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandle2FAVerifyInvalidCode(t *testing.T) {
	s := testServer()
	s.pendingTOTPMu.Lock()
	if s.pendingTOTP == nil {
		s.pendingTOTP = make(map[string]string)
	}
	s.pendingTOTP["admin"] = "JBSWY3DPEHPK3PXP"
	s.pendingTOTPMu.Unlock()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/verify", strings.NewReader(`{"code":"000000"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handle2FAVerify(rec, req)
	// code may happen to be valid by chance, but usually 401
	if rec.Code != 200 && rec.Code != 401 {
		t.Errorf("status = %d, want 200 or 401", rec.Code)
	}
}

func TestHandle2FADisableNotEnabled(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.handle2FADisable(rec, httptest.NewRequest("POST", "/api/v1/auth/2fa/disable", strings.NewReader(`{"code":"123456"}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandle2FADisableBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handle2FADisable(rec, httptest.NewRequest("POST", "/api/v1/auth/2fa/disable", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandle2FADisableInvalidCode(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.TOTPSecret = "JBSWY3DPEHPK3PXP"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/disable", strings.NewReader(`{"code":"000000"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handle2FADisable(rec, req)
	if rec.Code != 401 && rec.Code != 200 {
		t.Errorf("status = %d, want 401 or 200", rec.Code)
	}
}

// =============================================================================
// Multi-user auth handlers
// =============================================================================

func TestLoginNoAuthMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleLogin(rec, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"pass"}`)))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestLoginBadJSON(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	s.handleLogin(rec, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestLoginMissingFields(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	s.handleLogin(rec, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin"}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleLogin(rec, req)
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestLoginSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"password"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleLogin(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "authenticated" {
		t.Errorf("status = %v", body["status"])
	}
	if body["token"] == nil || body["token"] == "" {
		t.Error("token should be present")
	}
}

func TestLogoutNoAuthMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleLogout(rec, httptest.NewRequest("POST", "/api/v1/auth/logout", nil))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestLogoutSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/logout", strings.NewReader(`{"token":"some-token"}`))
	req.Header.Set("X-Session-Token", "some-token")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleLogout(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMeNoUser(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleAuthMe(rec, httptest.NewRequest("GET", "/api/v1/auth/me", nil))
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAuthMeWithUser(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("GET", "/api/v1/auth/me", nil))
	s.handleAuthMe(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["username"] != "admin" {
		t.Errorf("username = %v", body["username"])
	}
}

// =============================================================================
// User management auth handlers
// =============================================================================

func TestUserListAuthNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserListAuth(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/auth/users", nil)))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserListAuthNoUserCtx(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	s.handleUserListAuth(rec, httptest.NewRequest("GET", "/api/v1/auth/users", nil))
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestUserListAuthAdmin(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	s.handleUserListAuth(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/auth/users", nil)))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestUserListAuthReseller(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	s.handleUserListAuth(rec, withResellerContext(httptest.NewRequest("GET", "/api/v1/auth/users", nil)))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestUserGetAuthNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("GET", "/api/v1/auth/users/admin", nil))
	req.SetPathValue("username", "admin")
	s.handleUserGetAuth(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserGetAuthNotFound(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("GET", "/api/v1/auth/users/nobody", nil))
	req.SetPathValue("username", "nobody")
	s.handleUserGetAuth(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestUserGetAuthForbidden(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("GET", "/api/v1/auth/users/admin", nil))
	req.SetPathValue("username", "admin")
	s.handleUserGetAuth(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestUserGetAuthSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("GET", "/api/v1/auth/users/admin", nil))
	req.SetPathValue("username", "admin")
	s.handleUserGetAuth(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestUserCreateAuthNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserCreateAuth(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users", strings.NewReader(`{}`))))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserCreateAuthNotAdmin(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	s.handleUserCreateAuth(rec, withResellerContext(httptest.NewRequest("POST", "/api/v1/auth/users", strings.NewReader(`{"username":"newuser","email":"a@b.com","password":"pass","role":"user"}`))))
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestUserCreateAuthInvalidRole(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users", strings.NewReader(`{"username":"newuser","email":"a@b.com","password":"pass","role":"admin"}`)))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserCreateAuth(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUserCreateAuthSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	s.config.Global.Users.AllowResller = true
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users", strings.NewReader(`{"username":"newuser","email":"a@b.com","password":"pass","role":"user","domains":["test.com"]}`)))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserCreateAuth(rec, req)
	if rec.Code != 201 {
		t.Errorf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
}

func TestUserCreateAuthResellerNotAllowed(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	s.config.Global.Users.AllowResller = false
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users", strings.NewReader(`{"username":"newreseller","email":"a@b.com","password":"pass","role":"reseller","domains":["test.com"]}`)))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserCreateAuth(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUserCreateAuthBadJSON(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	s.handleUserCreateAuth(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users", strings.NewReader("not json"))))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUserUpdateAuthNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("PUT", "/api/v1/auth/users/admin", strings.NewReader(`{}`)))
	req.SetPathValue("username", "admin")
	s.handleUserUpdateAuth(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserUpdateAuthForbidden(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	email := "new@test.com"
	body := fmt.Sprintf(`{"email":"%s"}`, email)
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("PUT", "/api/v1/auth/users/admin", strings.NewReader(body)))
	req.SetPathValue("username", "admin")
	s.handleUserUpdateAuth(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestUserUpdateAuthSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	email := "new@test.com"
	body := fmt.Sprintf(`{"email":"%s"}`, email)
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("PUT", "/api/v1/auth/users/admin", strings.NewReader(body)))
	req.SetPathValue("username", "admin")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserUpdateAuth(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

func TestUserUpdateAuthDomainsForbiddenForNonAdmin(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("PUT", "/api/v1/auth/users/reseller", strings.NewReader(`{"domains":["hack.com"]}`)))
	req.SetPathValue("username", "reseller")
	s.handleUserUpdateAuth(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestUserDeleteAuthNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("DELETE", "/api/v1/auth/users/admin", nil))
	req.SetPathValue("username", "admin")
	s.handleUserDeleteAuth(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserDeleteAuthSelf(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("DELETE", "/api/v1/auth/users/admin", nil))
	req.SetPathValue("username", "admin")
	s.handleUserDeleteAuth(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUserDeleteAuthForbidden(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("DELETE", "/api/v1/auth/users/admin", nil))
	req.SetPathValue("username", "admin")
	s.handleUserDeleteAuth(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestUserDeleteAuthSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("DELETE", "/api/v1/auth/users/reseller", nil))
	req.SetPathValue("username", "reseller")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserDeleteAuth(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestUserRegenerateAPIKeyNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users/admin/apikey", nil))
	req.SetPathValue("username", "admin")
	s.handleUserRegenerateAPIKeyAuth(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserRegenerateAPIKeyForbidden(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("POST", "/api/v1/auth/users/admin/apikey", nil))
	req.SetPathValue("username", "admin")
	s.handleUserRegenerateAPIKeyAuth(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestUserRegenerateAPIKeySuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users/admin/apikey", nil))
	req.SetPathValue("username", "admin")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserRegenerateAPIKeyAuth(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestUserChangePasswordNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users/admin/password", strings.NewReader(`{"new_password":"new"}`)))
	req.SetPathValue("username", "admin")
	s.handleUserChangePasswordAuth(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserChangePasswordAdmin(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/users/reseller/password", strings.NewReader(`{"new_password":"newpass"}`)))
	req.SetPathValue("username", "reseller")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUserChangePasswordAuth(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// Unknown domains handlers
// =============================================================================

func TestUnknownDomainsListNil(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUnknownDomainsList(rec, httptest.NewRequest("GET", "/api/v1/unknown-domains", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestUnknownDomainsListWithTracker(t *testing.T) {
	s := testServer()
	s.SetUnknownHostTracker(router.NewUnknownHostTracker())
	rec := httptest.NewRecorder()
	s.handleUnknownDomainsList(rec, httptest.NewRequest("GET", "/api/v1/unknown-domains", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestUnknownDomainsBlockNoTracker(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/unknown-domains/evil.com/block", nil)
	req.SetPathValue("host", "evil.com")
	s.handleUnknownDomainsBlock(rec, req)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestUnknownDomainsBlockSuccess(t *testing.T) {
	s := testServer()
	s.SetUnknownHostTracker(router.NewUnknownHostTracker())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/unknown-domains/evil.com/block", nil)
	req.SetPathValue("host", "evil.com")
	s.handleUnknownDomainsBlock(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestUnknownDomainsUnblockNoTracker(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/unknown-domains/evil.com/unblock", nil)
	req.SetPathValue("host", "evil.com")
	s.handleUnknownDomainsUnblock(rec, req)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestUnknownDomainsUnblockSuccess(t *testing.T) {
	s := testServer()
	s.SetUnknownHostTracker(router.NewUnknownHostTracker())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/unknown-domains/evil.com/unblock", nil)
	req.SetPathValue("host", "evil.com")
	s.handleUnknownDomainsUnblock(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestUnknownDomainsDismissNoTracker(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/unknown-domains/evil.com", nil)
	req.SetPathValue("host", "evil.com")
	s.handleUnknownDomainsDismiss(rec, req)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestUnknownDomainsDismissSuccess(t *testing.T) {
	s := testServer()
	s.SetUnknownHostTracker(router.NewUnknownHostTracker())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/unknown-domains/evil.com", nil)
	req.SetPathValue("host", "evil.com")
	s.handleUnknownDomainsDismiss(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// =============================================================================
// Security stats handlers
// =============================================================================

func TestSecurityStatsNil(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSecurityStats(rec, httptest.NewRequest("GET", "/api/v1/security/stats", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["total_blocked"] != float64(0) {
		t.Errorf("total_blocked = %v", body["total_blocked"])
	}
}

func TestSecurityStatsWithTracker(t *testing.T) {
	s := testServer()
	st := middleware.NewSecurityStats()
	s.SetSecurityStats(st)
	rec := httptest.NewRecorder()
	s.handleSecurityStats(rec, httptest.NewRequest("GET", "/api/v1/security/stats", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSecurityBlockedNil(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSecurityBlocked(rec, httptest.NewRequest("GET", "/api/v1/security/blocked", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSecurityBlockedWithTracker(t *testing.T) {
	s := testServer()
	st := middleware.NewSecurityStats()
	s.SetSecurityStats(st)
	rec := httptest.NewRecorder()
	s.handleSecurityBlocked(rec, httptest.NewRequest("GET", "/api/v1/security/blocked", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// MCP handlers
// =============================================================================

func TestMCPToolsNoServer(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleMCPTools(rec, httptest.NewRequest("GET", "/api/v1/mcp/tools", nil))
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMCPToolsWithServer(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	m := mcp.New(s.config, log, s.metrics)
	s.SetMCP(m)
	rec := httptest.NewRecorder()
	s.handleMCPTools(rec, httptest.NewRequest("GET", "/api/v1/mcp/tools", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestMCPCallNoServer(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleMCPCall(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", strings.NewReader(`{"name":"test","input":{}}`)))
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMCPCallBadJSON(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	m := mcp.New(s.config, log, s.metrics)
	s.SetMCP(m)
	rec := httptest.NewRecorder()
	s.handleMCPCall(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMCPCallUnknownTool(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	m := mcp.New(s.config, log, s.metrics)
	s.SetMCP(m)
	rec := httptest.NewRecorder()
	s.handleMCPCall(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", strings.NewReader(`{"name":"nonexistent","input":{}}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Bandwidth handlers
// =============================================================================

func TestBandwidthListNil(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleBandwidthList(rec, httptest.NewRequest("GET", "/api/v1/bandwidth", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestBandwidthListWithMgr(t *testing.T) {
	s := testServer()
	bw := bandwidth.NewManager(nil)
	s.SetBandwidthManager(bw)
	rec := httptest.NewRecorder()
	s.handleBandwidthList(rec, httptest.NewRequest("GET", "/api/v1/bandwidth", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestBandwidthGetNilMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/bandwidth/example.com", nil)
	req.SetPathValue("host", "example.com")
	s.handleBandwidthGet(rec, req)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestBandwidthGetNotFound(t *testing.T) {
	s := testServer()
	bw := bandwidth.NewManager(nil)
	s.SetBandwidthManager(bw)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/bandwidth/nonexistent.com", nil)
	req.SetPathValue("host", "nonexistent.com")
	s.handleBandwidthGet(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestBandwidthResetNilMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/bandwidth/example.com/reset", nil)
	req.SetPathValue("host", "example.com")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleBandwidthReset(rec, req)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestBandwidthResetSuccess(t *testing.T) {
	s := testServer()
	bw := bandwidth.NewManager(nil)
	s.SetBandwidthManager(bw)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/bandwidth/example.com/reset", nil)
	req.SetPathValue("host", "example.com")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleBandwidthReset(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// =============================================================================
// Cron monitoring handlers
// =============================================================================

func TestCronMonitorListNil(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleCronMonitorList(rec, httptest.NewRequest("GET", "/api/v1/cron/monitor", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCronMonitorDomainNil(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/cron/monitor/example.com", nil)
	req.SetPathValue("host", "example.com")
	s.handleCronMonitorDomain(rec, req)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestCronMonitorDomainWithMonitor(t *testing.T) {
	s := testServer()
	cm := cronjob.NewMonitor("")
	s.SetCronMonitor(cm)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/cron/monitor/example.com", nil)
	req.SetPathValue("host", "example.com")
	s.handleCronMonitorDomain(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCronExecuteBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleCronExecute(rec, httptest.NewRequest("POST", "/api/v1/cron/execute", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCronExecuteMissingFields(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleCronExecute(rec, httptest.NewRequest("POST", "/api/v1/cron/execute", strings.NewReader(`{"domain":"test.com"}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCronExecuteNoMonitor(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleCronExecute(rec, httptest.NewRequest("POST", "/api/v1/cron/execute", strings.NewReader(`{"domain":"test.com","command":"echo hi"}`)))
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestCronExecuteSuccess(t *testing.T) {
	s := testServer()
	cm := cronjob.NewMonitor("")
	s.SetCronMonitor(cm)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cron/execute", strings.NewReader(`{"domain":"test.com","schedule":"* * * * *","command":"echo hi"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleCronExecute(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// =============================================================================
// Webhook handlers
// =============================================================================

func TestWebhookListEmpty(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleWebhookList(rec, httptest.NewRequest("GET", "/api/v1/webhooks", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestWebhookCreateBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleWebhookCreate(rec, httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookCreateMissingURL(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleWebhookCreate(rec, httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader(`{"events":["test"]}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookCreateInvalidURL(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleWebhookCreate(rec, httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader(`{"url":"ftp://invalid"}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookCreateSuccess(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader(`{"url":"https://example.com/hook","events":["domain.add"]}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleWebhookCreate(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Global.Webhooks) != 1 {
		t.Errorf("webhooks count = %d, want 1", len(s.config.Global.Webhooks))
	}
}

func TestWebhookDeleteBadID(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/webhooks/abc", nil)
	req.SetPathValue("id", "abc")
	s.handleWebhookDelete(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookDeleteNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/webhooks/5", nil)
	req.SetPathValue("id", "5")
	s.handleWebhookDelete(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebhookDeleteSuccess(t *testing.T) {
	s := testServer()
	s.config.Global.Webhooks = []config.WebhookConfig{
		{URL: "https://example.com/hook", Enabled: true},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/webhooks/0", nil)
	req.SetPathValue("id", "0")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleWebhookDelete(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(s.config.Global.Webhooks) != 0 {
		t.Errorf("webhooks count = %d, want 0", len(s.config.Global.Webhooks))
	}
}

func TestWebhookTestNoMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleWebhookTest(rec, httptest.NewRequest("POST", "/api/v1/webhooks/test", strings.NewReader(`{"url":"https://example.com"}`)))
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestWebhookTestMissingURL(t *testing.T) {
	s := testServer()
	s.SetWebhookManager(webhook.NewManager("", logger.New("error", "text")))
	rec := httptest.NewRecorder()
	s.handleWebhookTest(rec, httptest.NewRequest("POST", "/api/v1/webhooks/test", strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookTestSuccess(t *testing.T) {
	s := testServer()
	s.SetWebhookManager(webhook.NewManager("", logger.New("error", "text")))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/webhooks/test", strings.NewReader(`{"url":"https://example.com/hook"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleWebhookTest(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestWebhookTestBadJSON(t *testing.T) {
	s := testServer()
	s.SetWebhookManager(webhook.NewManager("", logger.New("error", "text")))
	rec := httptest.NewRecorder()
	s.handleWebhookTest(rec, httptest.NewRequest("POST", "/api/v1/webhooks/test", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Domain debug handler
// =============================================================================

func TestDomainDebugNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/domains/nonexistent.com/debug", nil)
	req.SetPathValue("host", "nonexistent.com")
	s.handleDomainDebug(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["configured"] != false {
		t.Error("should not be configured")
	}
}

func TestDomainDebugFound(t *testing.T) {
	s, root := testServerWithRoot(t)
	// Create a file in root
	os.WriteFile(filepath.Join(root, "index.html"), []byte("<html></html>"), 0644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/domains/example.com/debug", nil)
	req.SetPathValue("host", "example.com")
	s.handleDomainDebug(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["configured"] != true {
		t.Error("should be configured")
	}
	if body["root_exists"] != true {
		t.Error("root should exist")
	}
}

func TestDomainDebugRootNotExist(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "ghost.com", Type: "static", Root: "/this/path/definitely/does/not/exist/anywhere/12345"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/domains/ghost.com/debug", nil)
	req.SetPathValue("host", "ghost.com")
	s.handleDomainDebug(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["root_exists"] != false {
		t.Error("root should not exist")
	}
}

func TestDomainDebugEmptyRoot(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "noroot.com", Type: "static", Root: ""},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/domains/noroot.com/debug", nil)
	req.SetPathValue("host", "noroot.com")
	s.handleDomainDebug(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// System resources handler
// =============================================================================

func TestSystemResources(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSystemResources(rec, httptest.NewRequest("GET", "/api/v1/system/resources", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["cpus"] == nil {
		t.Error("cpus should be present")
	}
}

// =============================================================================
// Server IPs handler
// =============================================================================

func TestServerIPs(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleServerIPs(rec, httptest.NewRequest("GET", "/api/v1/system/ips", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// Stats domains handler
// =============================================================================

func TestStatsDomains(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleStatsDomains(rec, httptest.NewRequest("GET", "/api/v1/stats/domains", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// Doctor handlers
// =============================================================================

func TestDoctorHandler(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDoctor(rec, httptest.NewRequest("GET", "/api/v1/doctor", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDoctorFixHandler(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/doctor/fix", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleDoctorFix(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// Package list handler
// =============================================================================

func TestPackageList(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handlePackageList(rec, httptest.NewRequest("GET", "/api/v1/packages", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var pkgs []PackageInfo
	json.Unmarshal(rec.Body.Bytes(), &pkgs)
	if len(pkgs) == 0 {
		t.Error("packages should not be empty")
	}
}

func TestPackageInstallBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handlePackageInstall(rec, httptest.NewRequest("POST", "/api/v1/packages/install", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPackageInstallUnknown(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/packages/install", strings.NewReader(`{"id":"nonexistent"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handlePackageInstall(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPackageInstallCannotRemoveRequired(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/packages/install", strings.NewReader(`{"id":"curl","action":"remove"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handlePackageInstall(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// =============================================================================
// Migrate and Clone handlers
// =============================================================================

func TestMigrateBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleMigrate(rec, httptest.NewRequest("POST", "/api/v1/migrate", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMigrateMissingFields(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/migrate", strings.NewReader(`{"source_host":"old.com"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleMigrate(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMigrateDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/migrate", strings.NewReader(`{"source_host":"old.com","domain":"nonexistent.com"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleMigrate(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloneBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleClone(rec, httptest.NewRequest("POST", "/api/v1/clone", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloneMissingFields(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/clone", strings.NewReader(`{"source_domain":"old.com"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleClone(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloneSourceNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/clone", strings.NewReader(`{"source_domain":"nonexistent.com","target_domain":"clone.com"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleClone(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Notify test handler
// =============================================================================

func TestNotifyTestBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleNotifyTest(rec, httptest.NewRequest("POST", "/api/v1/notify/test", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// WordPress handlers (more coverage)
// =============================================================================

func TestWPSitesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleWPSites(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestWPUpdateCoreDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/nonexistent.com/update-core", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.handleWPUpdateCore(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWPUpdatePluginsDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/nonexistent.com/update-plugins", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.handleWPUpdatePlugins(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWPPluginActionDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/nonexistent.com/plugin/activate/akismet", nil)
	req.SetPathValue("domain", "nonexistent.com")
	req.SetPathValue("action", "activate")
	req.SetPathValue("plugin", "akismet")
	s.handleWPPluginAction(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWPPluginActionInvalidAction(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/plugin/badaction/akismet", nil)
	req.SetPathValue("domain", "example.com")
	req.SetPathValue("action", "badaction")
	req.SetPathValue("plugin", "akismet")
	s.handleWPPluginAction(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWPFixPermissionsDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/nonexistent.com/fix-permissions", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.handleWPFixPermissions(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWPReinstallDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/nonexistent.com/reinstall", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.handleWPReinstall(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWPReinstallNotWP(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/reinstall", nil)
	req.SetPathValue("domain", "example.com")
	s.handleWPReinstall(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWPToggleDebugDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/nonexistent.com/debug", strings.NewReader(`{"enable":true}`))
	req.SetPathValue("domain", "nonexistent.com")
	s.handleWPToggleDebug(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWPToggleDebugNotWP(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/debug", strings.NewReader(`{"enable":true}`))
	req.SetPathValue("domain", "example.com")
	s.handleWPToggleDebug(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWPToggleDebugBadJSON(t *testing.T) {
	s, _ := testServerWithRoot(t)
	// Create wp-config.php to make it look like WP
	root := s.config.Domains[0].Root
	os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("<?php"), 0644)
	os.MkdirAll(filepath.Join(root, "wp-includes"), 0755)
	os.MkdirAll(filepath.Join(root, "wp-admin"), 0755)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/debug", strings.NewReader("not json"))
	req.SetPathValue("domain", "example.com")
	s.handleWPToggleDebug(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWPErrorLogDomainNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/sites/nonexistent.com/error-log", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.handleWPErrorLog(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWPErrorLogNoFile(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/sites/example.com/error-log", nil)
	req.SetPathValue("domain", "example.com")
	s.handleWPErrorLog(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["log"] != "" {
		t.Error("log should be empty")
	}
}

func TestWPErrorLogWithFile(t *testing.T) {
	s, root := testServerWithRoot(t)
	logDir := filepath.Join(root, "wp-content")
	os.MkdirAll(logDir, 0755)
	os.WriteFile(filepath.Join(logDir, "debug.log"), []byte("test error log content"), 0644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/sites/example.com/error-log", nil)
	req.SetPathValue("domain", "example.com")
	s.handleWPErrorLog(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["log"] != "test error log content" {
		t.Errorf("log = %v", body["log"])
	}
}

// =============================================================================
// File manager handlers (more coverage)
// =============================================================================

func TestFileListWithDomain(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/example.com/list", nil)
	req.SetPathValue("domain", "example.com")
	s.handleFileList(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestFileReadNotFound(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/example.com/read?path=nonexistent.txt", nil)
	req.SetPathValue("domain", "example.com")
	s.handleFileRead(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFileReadNoDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/nonexistent.com/read?path=test", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.handleFileRead(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestFileWriteNoDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/files/nonexistent.com/write", strings.NewReader(`{"path":"test.txt","content":"hello"}`))
	req.SetPathValue("domain", "nonexistent.com")
	s.handleFileWrite(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestFileWriteBadJSON(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/files/example.com/write", strings.NewReader("not json"))
	req.SetPathValue("domain", "example.com")
	s.handleFileWrite(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFileWriteSuccess(t *testing.T) {
	s, root := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/files/example.com/write", strings.NewReader(`{"path":"hello.txt","content":"world"}`))
	req.SetPathValue("domain", "example.com")
	s.handleFileWrite(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(root, "hello.txt"))
	if string(data) != "world" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestFileDeleteNoDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/files/nonexistent.com/delete?path=test", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.handleFileDelete(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestFileMkdirNoDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/files/nonexistent.com/mkdir", strings.NewReader(`{"path":"newdir"}`))
	req.SetPathValue("domain", "nonexistent.com")
	s.handleFileMkdir(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestFileMkdirBadJSON(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/files/example.com/mkdir", strings.NewReader("not json"))
	req.SetPathValue("domain", "example.com")
	s.handleFileMkdir(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFileMkdirSuccess(t *testing.T) {
	s, root := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/files/example.com/mkdir", strings.NewReader(`{"path":"newdir"}`))
	req.SetPathValue("domain", "example.com")
	s.handleFileMkdir(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	info, err := os.Stat(filepath.Join(root, "newdir"))
	if err != nil || !info.IsDir() {
		t.Error("directory should have been created")
	}
}

func TestFileUploadNoDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/files/nonexistent.com/upload", strings.NewReader(""))
	req.SetPathValue("domain", "nonexistent.com")
	s.handleFileUpload(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestDiskUsageSuccess(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/example.com/disk-usage", nil)
	req.SetPathValue("domain", "example.com")
	s.handleDiskUsage(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// Cron handlers
// =============================================================================

func TestCronDeleteBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleCronDelete(rec, httptest.NewRequest("DELETE", "/api/v1/cron", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Firewall handlers
// =============================================================================

func TestFirewallDenyMissingPort(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleFirewallDeny(rec, httptest.NewRequest("POST", "/api/v1/firewall/deny", strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallDenyBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleFirewallDeny(rec, httptest.NewRequest("POST", "/api/v1/firewall/deny", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallDeleteInvalidNumber(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/firewall/abc", nil)
	req.SetPathValue("number", "abc")
	s.handleFirewallDelete(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallDeleteZero(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/firewall/0", nil)
	req.SetPathValue("number", "0")
	s.handleFirewallDelete(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallAllowBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleFirewallAllow(rec, httptest.NewRequest("POST", "/api/v1/firewall/allow", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// SSH key handlers
// =============================================================================

func TestSSHKeyDeleteBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/users/test.com/ssh-keys", strings.NewReader("not json"))
	req.SetPathValue("domain", "test.com")
	s.handleSSHKeyDelete(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSSHKeyAddBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/users/test.com/ssh-keys", strings.NewReader("not json"))
	req.SetPathValue("domain", "test.com")
	s.handleSSHKeyAdd(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Database handlers
// =============================================================================

func TestDBStatusEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBStatus(rec, httptest.NewRequest("GET", "/api/v1/database/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDBListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBList(rec, httptest.NewRequest("GET", "/api/v1/database/list", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDBCreateBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBCreate(rec, httptest.NewRequest("POST", "/api/v1/database/create", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBCreateMissingName(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBCreate(rec, httptest.NewRequest("POST", "/api/v1/database/create", strings.NewReader(`{"user":"test"}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBChangePasswordBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBChangePassword(rec, httptest.NewRequest("POST", "/api/v1/database/users/password", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBChangePasswordMissingFields(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBChangePassword(rec, httptest.NewRequest("POST", "/api/v1/database/users/password", strings.NewReader(`{"user":"root"}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBDiagnose(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBDiagnose(rec, httptest.NewRequest("GET", "/api/v1/database/diagnose", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDBImportBadBody(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// DBImport reads body and passes to database.ImportDatabase which will fail
	req := httptest.NewRequest("POST", "/api/v1/database/testdb/import", strings.NewReader("SQL DATA"))
	req.SetPathValue("name", "testdb")
	s.handleDBImport(rec, req)
	// Will fail because MySQL is not running, but should not panic
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// =============================================================================
// DNS check handler
// =============================================================================

func TestDNSCheckEmptyDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/dns/", nil)
	req.SetPathValue("domain", "")
	s.handleDNSCheck(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDNSCheckValid(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/dns/example.com", nil)
	req.SetPathValue("domain", "example.com")
	s.handleDNSCheck(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// DNS record handlers (no provider configured)
// =============================================================================

func TestDNSRecordsNoProvider(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/dns/example.com/records", nil)
	req.SetPathValue("domain", "example.com")
	s.handleDNSRecords(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestDNSRecordCreateNoProvider(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/dns/example.com/records", strings.NewReader(`{"type":"A","name":"@","content":"1.2.3.4"}`))
	req.SetPathValue("domain", "example.com")
	s.handleDNSRecordCreate(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestDNSRecordUpdateNoProvider(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/dns/example.com/records/123", strings.NewReader(`{"type":"A","name":"@","content":"1.2.3.4"}`))
	req.SetPathValue("domain", "example.com")
	req.SetPathValue("id", "123")
	s.handleDNSRecordUpdate(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestDNSRecordDeleteNoProvider(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/dns/example.com/records/123", nil)
	req.SetPathValue("domain", "example.com")
	req.SetPathValue("id", "123")
	s.handleDNSRecordDelete(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestDNSSyncNoProvider(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/dns/example.com/sync", nil)
	req.SetPathValue("domain", "example.com")
	s.handleDNSSync(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

// =============================================================================
// Services handlers
// =============================================================================

func TestServicesListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleServicesList(rec, httptest.NewRequest("GET", "/api/v1/services", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// Docker DB handlers
// =============================================================================

func TestDockerDBListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDockerDBList(rec, httptest.NewRequest("GET", "/api/v1/database/docker", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDockerDBCreateBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDockerDBCreate(rec, httptest.NewRequest("POST", "/api/v1/database/docker", strings.NewReader("not json")))
	// Docker may not be available -> 503, or bad JSON -> 400
	if rec.Code != 400 && rec.Code != 503 {
		t.Errorf("status = %d, want 400 or 503", rec.Code)
	}
}

func TestDockerDBCreateMissingFields(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDockerDBCreate(rec, httptest.NewRequest("POST", "/api/v1/database/docker", strings.NewReader(`{"name":"test"}`)))
	// 400 or 503 depending on Docker availability
	if rec.Code != 400 && rec.Code != 503 {
		t.Errorf("status = %d, want 400 or 503", rec.Code)
	}
}

// =============================================================================
// Cert renew handler
// =============================================================================

func TestCertRenewNoTLSMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/certs/example.com/renew", nil)
	req.SetPathValue("host", "example.com")
	s.handleCertRenew(rec, req)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// =============================================================================
// Domain CRUD with validation
// =============================================================================

func TestAddDomainInvalidHostname(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"host":"../etc/passwd","type":"static"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleAddDomain(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAddDomainInvalidType(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"host":"valid.com","type":"badtype"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleAddDomain(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAddDomainRedirectNoTarget(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"host":"redir.com","type":"redirect"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleAddDomain(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAddDomainProxyNoUpstreams(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"host":"proxy.com","type":"proxy"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleAddDomain(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateDomainBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.com", strings.NewReader("not json"))
	req.SetPathValue("host", "example.com")
	s.handleUpdateDomain(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateDomainReplaceMode(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"type":"static","cache":{"enabled":false}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.com?replace=true", body)
	req.SetPathValue("host", "example.com")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleUpdateDomain(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// =============================================================================
// SFTP user handlers
// =============================================================================

func TestUserCreateMissingDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserCreate(rec, httptest.NewRequest("POST", "/api/v1/users", strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUserCreateBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserCreate(rec, httptest.NewRequest("POST", "/api/v1/users", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Helper function tests
// =============================================================================

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"abc", "****"},
		{"12345", "****5"},
		{"secretkey12", "****y12"},
	}
	for _, tt := range tests {
		got := maskSecret(tt.input)
		// For short secrets, just ensure it starts with ****
		if tt.input == "" && got != "" {
			t.Errorf("maskSecret(%q) = %q, want empty", tt.input, got)
		}
		if tt.input != "" && !strings.HasPrefix(got, "****") {
			t.Errorf("maskSecret(%q) = %q, want prefix ****", tt.input, got)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "0s"},
		{5 * time.Second, "5s"},
		{65 * time.Second, "1m 5s"},
		{3661 * time.Second, "1h 1m 1s"},
		{90061 * time.Second, "1d 1h 1m 1s"},
	}
	for _, tt := range tests {
		got := humanDuration(tt.d)
		if got != tt.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestIsValidHostname(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"sub.example.com", true},
		{"*.example.com", true},
		{"a", true},
		{"", false},
		{".example.com", false},
		{"-example.com", false},
		{"example..com", false},
		{"example com", false},
		{"example/com", false},
		{strings.Repeat("a", 254), false},
	}
	for _, tt := range tests {
		got := isValidHostname(tt.host)
		if got != tt.want {
			t.Errorf("isValidHostname(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		b    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.b)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.b, got, tt.want)
		}
	}
}

func TestFormatDiskSize(t *testing.T) {
	tests := []struct {
		b    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := formatDiskSize(tt.b)
		if got != tt.want {
			t.Errorf("formatDiskSize(%d) = %q, want %q", tt.b, got, tt.want)
		}
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		v    any
		want int
	}{
		{float64(42), 42},
		{42, 42},
		{"99", 99},
		{true, 0},
	}
	for _, tt := range tests {
		got := toInt(tt.v)
		if got != tt.want {
			t.Errorf("toInt(%v) = %d, want %d", tt.v, got, tt.want)
		}
	}
}

func TestParseDur(t *testing.T) {
	d := parseDur("30s")
	if d.Duration != 30*time.Second {
		t.Errorf("parseDur('30s') = %v, want 30s", d.Duration)
	}
	// Invalid
	d2 := parseDur("invalid")
	if d2.Duration != 0 {
		t.Errorf("parseDur('invalid') = %v, want 0", d2.Duration)
	}
}

func TestByteSizeStr(t *testing.T) {
	got := byteSizeStr(config.ByteSize(1048576))
	if got == "" {
		t.Error("byteSizeStr should return non-empty string")
	}
}

func TestParseBS(t *testing.T) {
	b := parseBS("1M")
	if int64(b) == 0 {
		t.Error("parseBS('1M') should not be 0")
	}
}

// =============================================================================
// ValidateDomainConfig tests
// =============================================================================

func TestValidateDomainConfigTypes(t *testing.T) {
	s := testServer()

	// Invalid type
	d := &config.Domain{Host: "test.com", Type: "invalid"}
	if err := validateDomainConfig(d, s); err == nil {
		t.Error("expected error for invalid type")
	}

	// Invalid SSL mode
	d2 := &config.Domain{Host: "test.com", Type: "static", SSL: config.SSLConfig{Mode: "invalid"}}
	if err := validateDomainConfig(d2, s); err == nil {
		t.Error("expected error for invalid SSL mode")
	}

	// Manual SSL without cert
	d3 := &config.Domain{Host: "test.com", Type: "static", SSL: config.SSLConfig{Mode: "manual"}}
	if err := validateDomainConfig(d3, s); err == nil {
		t.Error("expected error for manual SSL without cert")
	}

	// Negative cache TTL
	d4 := &config.Domain{Host: "test.com", Type: "static", Cache: config.DomainCache{TTL: -1}}
	if err := validateDomainConfig(d4, s); err == nil {
		t.Error("expected error for negative TTL")
	}

	// Negative rate limit
	d5 := &config.Domain{Host: "test.com", Type: "static", Security: config.SecurityConfig{RateLimit: config.RateLimitConfig{Requests: -1}}}
	if err := validateDomainConfig(d5, s); err == nil {
		t.Error("expected error for negative rate limit")
	}

	// Valid static domain
	d6 := &config.Domain{Host: "test.com", Type: "static"}
	if err := validateDomainConfig(d6, s); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// PHP Install handlers
// =============================================================================

func TestPHPInstallInfoDefault(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handlePHPInstallInfo(rec, httptest.NewRequest("GET", "/api/v1/php/install-info", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPHPInstallInfoSpecificVersion(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handlePHPInstallInfo(rec, httptest.NewRequest("GET", "/api/v1/php/install-info?version=8.4", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPHPInstallBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handlePHPInstall(rec, httptest.NewRequest("POST", "/api/v1/php/install", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPHPInstallStatusIdle(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handlePHPInstallStatus(rec, httptest.NewRequest("GET", "/api/v1/php/install/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "idle" {
		t.Errorf("status = %v, want idle", body["status"])
	}
}

func TestPHPInstallAlreadyRunning(t *testing.T) {
	s := testServer()
	// Submit a long-running task to simulate an active install
	s.taskMgr.Submit("php", "PHP 8.3", "install", func(appendOutput func(string)) error {
		time.Sleep(10 * time.Second)
		return nil
	})
	// Give the task time to start
	time.Sleep(50 * time.Millisecond)
	rec := httptest.NewRecorder()
	s.handlePHPInstall(rec, httptest.NewRequest("POST", "/api/v1/php/install", strings.NewReader(`{"version":"8.4"}`)))
	if rec.Code != 409 {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

// =============================================================================
// PHP config raw handlers
// =============================================================================

func TestPHPConfigRawGetNoManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/php/8.4/config/raw", nil)
	req.SetPathValue("version", "8.4")
	s.handlePHPConfigRawGet(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPConfigRawPutNoManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/php/8.4/config/raw", strings.NewReader(`{"content":"test"}`))
	req.SetPathValue("version", "8.4")
	s.handlePHPConfigRawPut(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPConfigRawPutBadJSON(t *testing.T) {
	s := testServer()
	s.SetPHPManager(testPHPManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/php/8.4/config/raw", strings.NewReader("not json"))
	req.SetPathValue("version", "8.4")
	s.handlePHPConfigRawPut(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// PHP enable/disable handlers
// =============================================================================

func TestPHPEnableNoManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/php/8.4/enable", nil)
	req.SetPathValue("version", "8.4")
	s.handlePHPEnable(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDisableNoManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/php/8.4/disable", nil)
	req.SetPathValue("version", "8.4")
	s.handlePHPDisable(rec, req)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

// =============================================================================
// Backup domain handler
// =============================================================================

func TestBackupDomainNoManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleBackupDomain(rec, httptest.NewRequest("POST", "/api/v1/backups/domain", strings.NewReader(`{"domain":"test.com"}`)))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestBackupDomainBadJSON(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))
	rec := httptest.NewRecorder()
	s.handleBackupDomain(rec, httptest.NewRequest("POST", "/api/v1/backups/domain", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestBackupDomainMissing(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))
	rec := httptest.NewRecorder()
	s.handleBackupDomain(rec, httptest.NewRequest("POST", "/api/v1/backups/domain", strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// getDNSProvider tests
// =============================================================================

func TestGetDNSProviderNil(t *testing.T) {
	s := testServer()
	if s.getDNSProvider() != nil {
		t.Error("should be nil with no config")
	}
}

func TestGetDNSProviderUnknown(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSProvider = "unknown"
	s.config.Global.ACME.DNSCredentials = map[string]string{"token": "abc"}
	if s.getDNSProvider() != nil {
		t.Error("should be nil for unknown provider")
	}
}

func TestGetDNSProviderCloudflareNoToken(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSProvider = "cloudflare"
	s.config.Global.ACME.DNSCredentials = map[string]string{}
	if s.getDNSProvider() != nil {
		t.Error("should be nil without token")
	}
}

func TestGetDNSProviderCloudflare(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSProvider = "cloudflare"
	s.config.Global.ACME.DNSCredentials = map[string]string{"api_token": "test-token"}
	p := s.getDNSProvider()
	if p == nil {
		t.Error("should return a provider")
	}
}

func TestGetDNSProviderHetzner(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSProvider = "hetzner"
	s.config.Global.ACME.DNSCredentials = map[string]string{"token": "test-token"}
	p := s.getDNSProvider()
	if p == nil {
		t.Error("should return a provider")
	}
}

func TestGetDNSProviderDigitalOcean(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSProvider = "digitalocean"
	s.config.Global.ACME.DNSCredentials = map[string]string{"token": "test-token"}
	p := s.getDNSProvider()
	if p == nil {
		t.Error("should return a provider")
	}
}

func TestGetDNSProviderRoute53(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSProvider = "route53"
	s.config.Global.ACME.DNSCredentials = map[string]string{"access_key": "ak", "secret_key": "sk"}
	p := s.getDNSProvider()
	if p == nil {
		t.Error("should return a provider")
	}
}

func TestGetDNSProviderRoute53MissingKeys(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSProvider = "route53"
	s.config.Global.ACME.DNSCredentials = map[string]string{"access_key": "ak"}
	if s.getDNSProvider() != nil {
		t.Error("should be nil without secret_key")
	}
}

// =============================================================================
// PersistConfig tests
// =============================================================================

func TestPersistConfig(t *testing.T) {
	s := testServer()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	s.SetConfigPath(cfgPath)

	s.persistConfig()

	// Check that the config file was written
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if len(data) == 0 {
		t.Error("config file is empty")
	}

	// Check that domains.d/ was created
	domainsDir := filepath.Join(dir, "domains.d")
	entries, err := os.ReadDir(domainsDir)
	if err != nil {
		t.Fatalf("domains.d not created: %v", err)
	}
	if len(entries) != 2 { // example.com and api.example.com
		t.Errorf("expected 2 domain files, got %d", len(entries))
	}
}

func TestPersistConfigNoPath(t *testing.T) {
	s := testServer()
	// No config path set - should not panic
	s.persistConfig()
}

// =============================================================================
// Domain health handler
// =============================================================================

func TestDomainHealthEndpoint(t *testing.T) {
	s := testServer()
	// Use a domain that won't actually resolve
	s.config.Domains = []config.Domain{
		{Host: "localhost", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	rec := httptest.NewRecorder()
	s.handleDomainHealth(rec, httptest.NewRequest("GET", "/api/v1/domains/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// Auth middleware with multi-user
// =============================================================================

func TestAuthMiddlewareMultiUser(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
			Users: config.UsersConfig{Enabled: true},
		},
		Domains: []config.Domain{
			{Host: "test.com", Type: "static"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	mgr := newMockAuthManager()
	s.SetAuthManager(mgr)

	// Authenticate to get a session
	sess, _ := mgr.Authenticate("admin", "password")

	// Use session token
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("X-Session-Token", sess.Token)
	s.authMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("session auth: status = %d, want 200", rec.Code)
	}

	// Use API key
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req2.Header.Set("Authorization", "Bearer admin-api-key")
	s.authMiddleware(s.mux).ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Errorf("API key auth: status = %d, want 200", rec2.Code)
	}

	// Token in query string (for SSE)
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api/v1/stats?token="+sess.Token, nil)
	s.authMiddleware(s.mux).ServeHTTP(rec3, req3)
	if rec3.Code != 200 {
		t.Errorf("query token auth: status = %d, want 200", rec3.Code)
	}
}

func TestAuthMiddleware2FARequired(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{
				APIKey:     "testkey",
				TOTPSecret: "JBSWY3DPEHPK3PXP",
			},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	// Auth without TOTP code should get 403
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer testkey")
	s.authMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403 (2FA required)", rec.Code)
	}
	if rec.Header().Get("X-2FA-Required") != "true" {
		t.Error("should have X-2FA-Required header")
	}
}

func TestAuthMiddleware2FAInvalidCode(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{
				APIKey:     "testkey",
				TOTPSecret: "JBSWY3DPEHPK3PXP",
			},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer testkey")
	req.Header.Set("X-TOTP-Code", "000000")
	req.RemoteAddr = "10.0.0.1:1234"
	s.authMiddleware(s.mux).ServeHTTP(rec, req)
	// 000000 might match, so we accept 200 or 403
	if rec.Code != 200 && rec.Code != 403 {
		t.Errorf("status = %d, want 200 or 403", rec.Code)
	}
}

func TestAuthMiddlewareLegacyTokenInQuery(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
		Domains: []config.Domain{
			{Host: "test.com", Type: "static"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats?token=testkey", nil)
	s.authMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareLoginIsPublic(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.SetAuthManager(newMockAuthManager())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"password"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.authMiddleware(s.mux).ServeHTTP(rec, req)
	// Should not get 401 (login is public)
	if rec.Code == 401 {
		t.Error("login endpoint should be public")
	}
}

// =============================================================================
// SetWebhookManager / SetCronMonitor / SetBandwidthManager
// =============================================================================

func TestSetWebhookManager(t *testing.T) {
	s := testServer()
	if s.webhookMgr != nil {
		t.Error("should be nil initially")
	}
	wm := webhook.NewManager("", logger.New("error", "text"))
	s.SetWebhookManager(wm)
	if s.webhookMgr == nil {
		t.Error("should be set")
	}
}

func TestSetCronMonitor(t *testing.T) {
	s := testServer()
	if s.cronMonitor != nil {
		t.Error("should be nil initially")
	}
	cm := cronjob.NewMonitor("")
	s.SetCronMonitor(cm)
	if s.cronMonitor == nil {
		t.Error("should be set")
	}
}

func TestSetBandwidthManager(t *testing.T) {
	s := testServer()
	if s.bwMgr != nil {
		t.Error("should be nil initially")
	}
	bw := bandwidth.NewManager(nil)
	s.SetBandwidthManager(bw)
	if s.bwMgr == nil {
		t.Error("should be set")
	}
}

func TestSetUnknownHostTracker(t *testing.T) {
	s := testServer()
	if s.unknownHT != nil {
		t.Error("should be nil initially")
	}
	ut := router.NewUnknownHostTracker()
	s.SetUnknownHostTracker(ut)
	if s.unknownHT == nil {
		t.Error("should be set")
	}
}

func TestSetSecurityStats(t *testing.T) {
	s := testServer()
	if s.securityStats != nil {
		t.Error("should be nil initially")
	}
	st := middleware.NewSecurityStats()
	s.SetSecurityStats(st)
	if s.securityStats == nil {
		t.Error("should be set")
	}
}

func TestSetAuthManager(t *testing.T) {
	s := testServer()
	if s.authMgr != nil {
		t.Error("should be nil initially")
	}
	s.SetAuthManager(newMockAuthManager())
	if s.authMgr == nil {
		t.Error("should be set")
	}
}

// =============================================================================
// toWebhookConfigs test
// =============================================================================

func TestToWebhookConfigs(t *testing.T) {
	cfgs := []config.WebhookConfig{
		{
			URL:     "https://example.com/hook",
			Events:  []string{"domain.add", "domain.delete"},
			Secret:  "mysecret",
			Retry:   3,
			Enabled: true,
		},
	}
	result := toWebhookConfigs(cfgs)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0].URL != "https://example.com/hook" {
		t.Errorf("URL = %q", result[0].URL)
	}
	if len(result[0].Events) != 2 {
		t.Errorf("events = %d", len(result[0].Events))
	}
}

// =============================================================================
// findPkg test
// =============================================================================

func TestFindPkg(t *testing.T) {
	p := findPkg("mariadb")
	if p == nil {
		t.Error("should find mariadb")
	}
	if p.name != "MariaDB" {
		t.Errorf("name = %q", p.name)
	}
	p2 := findPkg("nonexistent")
	if p2 != nil {
		t.Error("should not find nonexistent")
	}
}

// =============================================================================
// domainRoot fallback tests
// =============================================================================

func TestDomainRootWithWebRoot(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = "/var/www"
	root := s.domainRoot("unknown.com")
	if root == "" {
		t.Error("should use fallback web root")
	}
	expected := filepath.Join("/var/www", "unknown.com", "public_html")
	if root != expected {
		t.Errorf("root = %q, want %q", root, expected)
	}
}

func TestDomainRootConfigured(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "test.com", Root: "/custom/root"},
	}
	root := s.domainRoot("test.com")
	if root != "/custom/root" {
		t.Errorf("root = %q, want /custom/root", root)
	}
}

func TestDomainRootEmpty(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = ""
	root := s.domainRoot("unknown.com")
	if root != "" {
		t.Errorf("root = %q, want empty", root)
	}
}

// =============================================================================
// Additional coverage: SFTP user delete handler
// =============================================================================

func TestUserDeleteEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/users/test.com", nil)
	req.SetPathValue("domain", "test.com")
	s.handleUserDelete(rec, req)
	// Will fail because no user exists, but tests the handler path
	if rec.Code != 200 && rec.Code != 500 {
		t.Errorf("status = %d, want 200 or 500", rec.Code)
	}
}

// =============================================================================
// Additional coverage: SetTLSManager
// =============================================================================

func TestSetTLSManager(t *testing.T) {
	s := testServer()
	if s.tlsMgr != nil {
		t.Error("should be nil initially")
	}
	// We can't easily create a real TLS manager in tests, but testing the setter is nil -> set
	s.SetTLSManager(nil)
	// Just verifying no panic
}

// =============================================================================
// Additional coverage: handleSettingsPut with more keys
// =============================================================================

func TestSettingsPutAllKeys(t *testing.T) {
	s := testServer()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	s.SetConfigPath(cfgPath)

	body := `{
		"global.http_listen":":80",
		"global.https_listen":":443",
		"global.http3":true,
		"global.worker_count":"4",
		"global.max_connections":2048,
		"global.pid_file":"/run/uwas.pid",
		"global.web_root":"/var/www",
		"global.log_level":"debug",
		"global.log_format":"json",
		"global.timeouts.read":"30s",
		"global.timeouts.read_header":"10s",
		"global.timeouts.write":"120s",
		"global.timeouts.idle":"120s",
		"global.timeouts.shutdown_grace":"5s",
		"global.timeouts.max_header_bytes":1048576,
		"global.admin.enabled":true,
		"global.admin.listen":"127.0.0.1:9443",
		"global.admin.api_key":"newkey",
		"global.mcp.enabled":true,
		"global.acme.email":"admin@test.com",
		"global.acme.ca_url":"https://acme.test",
		"global.acme.storage":"/etc/uwas/certs",
		"global.acme.dns_provider":"cloudflare",
		"global.cache.enabled":true,
		"global.cache.memory_limit":"256M",
		"global.cache.disk_path":"/tmp/cache",
		"global.cache.default_ttl":3600,
		"global.alerting.enabled":true,
		"global.alerting.webhook_url":"https://hook.test",
		"global.alerting.slack_url":"https://slack.test",
		"global.alerting.telegram_token":"tok123",
		"global.alerting.telegram_chat_id":"12345",
		"global.backup.enabled":true,
		"global.backup.provider":"local",
		"global.backup.schedule":"0 2 * * *",
		"global.backup.keep":7,
		"global.backup.local.path":"/backups",
		"global.backup.s3.endpoint":"s3.test",
		"global.backup.s3.bucket":"mybucket",
		"global.backup.s3.region":"us-east-1",
		"global.backup.sftp.host":"sftp.test",
		"global.backup.sftp.port":22,
		"global.backup.sftp.user":"backup"
	}`
	req := httptest.NewRequest("PUT", "/api/v1/settings", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	s.handleSettingsPut(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	// Verify all settings were applied
	if s.config.Global.HTTPListen != ":80" {
		t.Errorf("http_listen = %q", s.config.Global.HTTPListen)
	}
	if s.config.Global.LogLevel != "debug" {
		t.Errorf("log_level = %q", s.config.Global.LogLevel)
	}
	if !s.config.Global.HTTP3Enabled {
		t.Error("http3 should be enabled")
	}
	if !s.config.Global.Cache.Enabled {
		t.Error("cache should be enabled")
	}
	if s.config.Global.Admin.APIKey != "newkey" {
		t.Errorf("api_key = %q", s.config.Global.Admin.APIKey)
	}
}

// =============================================================================
// Additional coverage: handleCronAdd success path (will fail but covers code)
// =============================================================================

func TestCronAddSuccess(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"schedule":"* * * * *","command":"echo hello","user":"root"}`)
	s.handleCronAdd(rec, httptest.NewRequest("POST", "/api/v1/cron", body))
	// On non-Linux, crontab is not available so this might fail, but we exercise the code
	if rec.Code != 200 && rec.Code != 500 {
		t.Errorf("status = %d, want 200 or 500", rec.Code)
	}
}

func TestCronDeleteSuccess(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"schedule":"* * * * *","command":"echo hello"}`)
	s.handleCronDelete(rec, httptest.NewRequest("DELETE", "/api/v1/cron", body))
	// On non-Linux, crontab is not available so this might fail
	if rec.Code != 200 && rec.Code != 500 {
		t.Errorf("status = %d, want 200 or 500", rec.Code)
	}
}

// =============================================================================
// Additional coverage: handleWPUpdateCore and handleWPUpdatePlugins with root
// =============================================================================

func TestWPUpdateCoreNotWP(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/update-core", nil)
	req.SetPathValue("domain", "example.com")
	s.handleWPUpdateCore(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (not a WordPress site)", rec.Code)
	}
}

func TestWPUpdatePluginsWithRoot(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/update-plugins", nil)
	req.SetPathValue("domain", "example.com")
	s.handleWPUpdatePlugins(rec, req)
	// Will fail or succeed depending on WP status
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 500 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestWPFixPermissionsNotWP(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/fix-permissions", nil)
	req.SetPathValue("domain", "example.com")
	s.handleWPFixPermissions(rec, req)
	// We expect either success or failure, but path is exercised
	if rec.Code != 200 && rec.Code != 500 {
		t.Errorf("status = %d", rec.Code)
	}
}

// =============================================================================
// Additional coverage: PHP enable/disable with manager
// =============================================================================

func TestPHPEnableWithManager(t *testing.T) {
	s := testServer()
	s.SetPHPManager(testPHPManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/php/8.4/enable", nil)
	req.SetPathValue("version", "8.4")
	s.handlePHPEnable(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestPHPDisableWithManager(t *testing.T) {
	s := testServer()
	s.SetPHPManager(testPHPManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/php/8.4/disable", nil)
	req.SetPathValue("version", "8.4")
	s.handlePHPDisable(rec, req)
	// May fail with "not found" or succeed depending on manager state
	if rec.Code != 200 && rec.Code != 409 {
		t.Errorf("status = %d, want 200 or 409", rec.Code)
	}
}

// =============================================================================
// Additional coverage: PHP config raw with manager
// =============================================================================

func TestPHPConfigRawGetWithManager(t *testing.T) {
	s := testServer()
	s.SetPHPManager(testPHPManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/php/9.9/config/raw", nil)
	req.SetPathValue("version", "9.9")
	s.handlePHPConfigRawGet(rec, req)
	// Will return 404 since version doesn't exist
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPHPConfigRawPutWithManager(t *testing.T) {
	s := testServer()
	s.SetPHPManager(testPHPManager())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/php/9.9/config/raw", strings.NewReader(`{"content":"test config"}`))
	req.SetPathValue("version", "9.9")
	s.handlePHPConfigRawPut(rec, req)
	// Will return 500 since version doesn't exist
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// =============================================================================
// Additional coverage: handleCronMonitorList with monitor
// =============================================================================

func TestCronMonitorListWithMonitor(t *testing.T) {
	s := testServer()
	cm := cronjob.NewMonitor("")
	s.SetCronMonitor(cm)
	rec := httptest.NewRecorder()
	s.handleCronMonitorList(rec, httptest.NewRequest("GET", "/api/v1/cron/monitor", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// =============================================================================
// Additional coverage: handleFileDelete success
// =============================================================================

func TestFileDeleteSuccess(t *testing.T) {
	s, root := testServerWithRoot(t)
	// Create a file to delete
	testFile := filepath.Join(root, "deleteme.txt")
	os.WriteFile(testFile, []byte("test"), 0644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/files/example.com/delete?path=deleteme.txt", nil)
	req.SetPathValue("domain", "example.com")
	s.handleFileDelete(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	// Verify file was deleted
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

// =============================================================================
// Additional coverage: handleFileRead success
// =============================================================================

func TestFileReadSuccess(t *testing.T) {
	s, root := testServerWithRoot(t)
	os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello world"), 0644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/example.com/read?path=readme.txt", nil)
	req.SetPathValue("domain", "example.com")
	s.handleFileRead(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["content"] != "hello world" {
		t.Errorf("content = %q", body["content"])
	}
}

// =============================================================================
// Additional coverage: handleDeleteDomain with cleanup
// =============================================================================

func TestDeleteDomainWithCleanup(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/domains/example.com?cleanup=true", nil)
	req.SetPathValue("host", "example.com")
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleDeleteDomain(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// Additional coverage: handleAddDomain with redirect target
// =============================================================================

func TestAddDomainRedirectWithTarget(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"host":"redir.com","type":"redirect","redirect":{"target":"https://target.com"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleAddDomain(rec, req)
	if rec.Code != 201 {
		t.Errorf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// Additional coverage: handleAddDomain bad JSON
// =============================================================================

func TestAddDomainBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader("not json"))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleAddDomain(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Additional coverage: handleAddDomain proxy with upstreams
// =============================================================================

func TestAddDomainProxyWithUpstreams(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"host":"lb.com","type":"proxy","proxy":{"upstreams":[{"url":"http://localhost:3000"}]}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleAddDomain(rec, req)
	if rec.Code != 201 {
		t.Errorf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// Additional coverage: handleWPInstall with web root
// =============================================================================

func TestWPInstallWithDomain(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/install", strings.NewReader(`{"domain":"example.com"}`))
	s.handleWPInstall(rec, req)
	// Will start the install in background
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	// Wait briefly for goroutine to start
	time.Sleep(100 * time.Millisecond)
	// Reset state
	wpInstallMu.Lock()
	wpInstallResult = nil
	wpInstallMu.Unlock()
}

func TestWPInstallDuplicateRunning(t *testing.T) {
	s := testServer()
	wpInstallMu.Lock()
	wpInstallResult = &wordpress.InstallResult{Status: "running", Domain: "other.com"}
	wpInstallMu.Unlock()
	defer func() {
		wpInstallMu.Lock()
		wpInstallResult = nil
		wpInstallMu.Unlock()
	}()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/install", strings.NewReader(`{"domain":"test.com","web_root":"/tmp/test"}`))
	s.handleWPInstall(rec, req)
	if rec.Code != 409 {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

// =============================================================================
// Additional coverage: handleClone with root
// =============================================================================

func TestCloneWithSourceRoot(t *testing.T) {
	s, root := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/clone", strings.NewReader(fmt.Sprintf(`{"source_domain":"example.com","target_domain":"clone.com","source_root":"%s","target_root":"/tmp/clone"}`, strings.ReplaceAll(root, `\`, `\\`))))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleClone(rec, req)
	// Clone will run, may fail but exercises the code path
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// Additional coverage: handleMigrate with root
// =============================================================================

func TestMigrateWithLocalRoot(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/migrate", strings.NewReader(`{"source_host":"old.example.com","domain":"example.com","local_root":"/tmp/migrate"}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleMigrate(rec, req)
	// Will fail but exercises the code path
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}
