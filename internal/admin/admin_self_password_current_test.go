package admin

// F28800: POST /auth/users/{username}/password let an admin set their OWN
// password without proving the current one (the admin branch called
// UpdateUser directly), while handleUserUpdateAuth refuses exactly that for
// every role because a hijacked session would otherwise turn into persistent
// account takeover. Self-service changes must verify the current password for
// admins too; an admin resetting ANOTHER user's password is unchanged.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

type passwordCallRecorder struct {
	*mockAuthManager
	updates []string
	changes []string
}

func (p *passwordCallRecorder) UpdateUser(username string, u *auth.User) error {
	if u.Password != "" {
		p.updates = append(p.updates, username)
	}
	return p.mockAuthManager.UpdateUser(username, u)
}

func (p *passwordCallRecorder) ChangePassword(username, cur, next string) error {
	p.changes = append(p.changes, username)
	return p.mockAuthManager.ChangePassword(username, cur, next)
}

func adminSelfPasswordRequest(target, body string) *http.Request {
	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	req.SetPathValue("username", target)
	return withAdminContext(req)
}

func TestAdminSelfPasswordChangeRequiresCurrentPassword(t *testing.T) {
	s := testServer()
	rec := &passwordCallRecorder{mockAuthManager: newMockAuthManager()}
	s.SetAuthManager(rec)

	// Hijacked admin session: no current password supplied.
	w := httptest.NewRecorder()
	s.handleUserChangePasswordAuth(w, adminSelfPasswordRequest("admin", `{"new_password":"S3cure-Passw0rd!"}`))
	t.Logf("EXPECTED: 400, no password write\nACTUAL: %d, updates=%v", w.Code, rec.updates)
	if w.Code != http.StatusBadRequest || len(rec.updates) != 0 {
		t.Fatalf("PROBLEM CONFIRMED: admin changed own password without current_password (status %d)", w.Code)
	}

	// Wrong current password is refused.
	w = httptest.NewRecorder()
	s.handleUserChangePasswordAuth(w, adminSelfPasswordRequest("admin", `{"current_password":"wrong","new_password":"S3cure-Passw0rd!"}`))
	if w.Code != http.StatusUnauthorized || len(rec.updates) != 0 {
		t.Fatalf("wrong current password: status %d updates %v, want 401 and no write", w.Code, rec.updates)
	}

	// Correct current password succeeds through ChangePassword.
	w = httptest.NewRecorder()
	s.handleUserChangePasswordAuth(w, adminSelfPasswordRequest("admin", `{"current_password":"password","new_password":"S3cure-Passw0rd!"}`))
	if w.Code != http.StatusOK || len(rec.changes) != 2 {
		t.Fatalf("correct current password: status %d changes %v, want 200 (2nd ChangePassword call)", w.Code, rec.changes)
	}
}

func TestAdminResetsOtherUserPasswordWithoutCurrent(t *testing.T) {
	s := testServer()
	rec := &passwordCallRecorder{mockAuthManager: newMockAuthManager()}
	s.SetAuthManager(rec)

	w := httptest.NewRecorder()
	s.handleUserChangePasswordAuth(w, adminSelfPasswordRequest("reseller", `{"new_password":"S3cure-Passw0rd!"}`))
	if w.Code != http.StatusOK || len(rec.updates) != 1 || rec.updates[0] != "reseller" {
		t.Fatalf("admin reset of another user: status %d updates %v, want 200", w.Code, rec.updates)
	}
}
