package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// =============================================================================
// SFTP user create/delete: auth + validation branches
// =============================================================================

func TestGrpG_UserCreateRequiresAdmin(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserCreate(rec, withResellerContext(httptest.NewRequest("POST", "/x", strings.NewReader("{}"))))
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller = %d, want 403", rec.Code)
	}
}

func TestGrpG_UserCreateInvalidJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserCreate(rec, withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{bad"))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}
}

func TestGrpG_UserCreateMissingDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleUserCreate(rec, withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{}"))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing domain = %d, want 400", rec.Code)
	}
}

func TestGrpG_UserDeleteResellerForbidden(t *testing.T) {
	s := testServer()
	s.authMgr = newMockAuthManager()
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("DELETE", "/x", nil))
	req.SetPathValue("domain", "example.com") // not owned by reseller
	s.handleUserDelete(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller delete = %d, want 403", rec.Code)
	}
}

func TestGrpG_UserDeleteAppRequiresAdmin(t *testing.T) {
	s := testServer()
	s.authMgr = newMockAuthManager()
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("DELETE", "/x", nil))
	req.SetPathValue("domain", "app-myapp.uwas.local") // app target -> admin required
	s.handleUserDelete(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller app delete = %d, want 403", rec.Code)
	}
}

// =============================================================================
// Auth ticket issuance
// =============================================================================

func TestGrpG_AuthTicketFromBearer(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/ticket", nil))
	req.Header.Set("Authorization", "Bearer my-real-token")
	s.handleAuthTicket(rec, req)
	if rec.Code != 200 {
		t.Fatalf("ticket = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ticket") {
		t.Errorf("response missing ticket: %s", rec.Body.String())
	}
}

func TestGrpG_AuthTicketFromSessionToken(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/ticket", nil))
	req.Header.Set("X-Session-Token", "sess-token")
	s.handleAuthTicket(rec, req)
	if rec.Code != 200 {
		t.Fatalf("ticket = %d body=%s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// requirePin behavior
// =============================================================================

func TestGrpG_RequirePinNoneConfigured(t *testing.T) {
	s := testServer() // no pin
	rec := httptest.NewRecorder()
	if !s.requirePin(rec, httptest.NewRequest("GET", "/x", nil)) {
		t.Error("requirePin should allow when no pin configured")
	}
}

func TestGrpG_RequirePinConfigured(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.PinCode = "1234"

	// missing pin -> false, 403
	rec := httptest.NewRecorder()
	if s.requirePin(rec, httptest.NewRequest("GET", "/x", nil)) {
		t.Error("requirePin should reject when pin missing")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rec.Code)
	}

	// wrong pin -> false
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Pin-Code", "9999")
	if s.requirePin(rec, req) {
		t.Error("requirePin should reject wrong pin")
	}

	// correct pin via header -> true
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Pin-Code", "1234")
	if !s.requirePin(rec, req) {
		t.Error("requirePin should accept correct pin header")
	}

	// correct pin via query param -> true
	rec = httptest.NewRecorder()
	if !s.requirePin(rec, httptest.NewRequest("GET", "/x?pin=1234", nil)) {
		t.Error("requirePin should accept correct pin query param")
	}
}

// =============================================================================
// requireAdmin / requireDomainAccess / canAccessDomain
// =============================================================================

func TestGrpG_RequireAdmin(t *testing.T) {
	s := testServer()

	// no context -> 403
	rec := httptest.NewRecorder()
	if s.requireAdmin(rec, httptest.NewRequest("GET", "/x", nil)) {
		t.Error("requireAdmin should reject no-context")
	}

	// admin context -> true
	rec = httptest.NewRecorder()
	if !s.requireAdmin(rec, withAdminContext(httptest.NewRequest("GET", "/x", nil))) {
		t.Error("requireAdmin should accept admin")
	}

	// reseller -> false
	rec = httptest.NewRecorder()
	if s.requireAdmin(rec, withResellerContext(httptest.NewRequest("GET", "/x", nil))) {
		t.Error("requireAdmin should reject reseller")
	}
}

func TestGrpG_CanAccessDomain(t *testing.T) {
	// authMgr nil -> always true
	s := testServer()
	if !s.canAccessDomain(httptest.NewRequest("GET", "/x", nil), "anything.com") {
		t.Error("nil authMgr should allow all")
	}

	// with authMgr: admin allowed, reseller only own domains
	s.authMgr = newMockAuthManager()
	if !s.canAccessDomain(withAdminContext(httptest.NewRequest("GET", "/x", nil)), "any.com") {
		t.Error("admin should access any domain")
	}
	if s.canAccessDomain(withResellerContext(httptest.NewRequest("GET", "/x", nil)), "notmine.com") {
		t.Error("reseller should not access unowned domain")
	}
	if !s.canAccessDomain(withResellerContext(httptest.NewRequest("GET", "/x", nil)), "reseller.com") {
		t.Error("reseller should access owned domain")
	}
}

func TestGrpG_RequireDomainAccessForbidden(t *testing.T) {
	s := testServer()
	s.authMgr = newMockAuthManager()
	rec := httptest.NewRecorder()
	if s.requireDomainAccess(rec, withResellerContext(httptest.NewRequest("GET", "/x", nil)), "notmine.com", "test.action") {
		t.Error("requireDomainAccess should deny")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rec.Code)
	}
}
