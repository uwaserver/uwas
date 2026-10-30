package admin

// F39100: POST /auth/users/{username}/apikey replaced the caller's own API key
// without proving the password. The key is a credential that outlives every
// session (a password change drops sessions, not keys), so a hijacked session
// could mint itself a permanent one - the case handleUserChangePasswordAuth
// (F28800) and handleUserUpdateAuth already refuse for the password itself.
// Replacing your own key now proves the current password for every role; an
// admin replacing ANOTHER user's key is unchanged.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type apiKeyRecorder struct {
	*mockAuthManager
	regenerated []string
}

func (a *apiKeyRecorder) VerifyPassword(_ string, password string) error {
	return a.mockAuthManager.ChangePassword("", password, "")
}

func (a *apiKeyRecorder) RegenerateAPIKey(username string) (string, error) {
	a.regenerated = append(a.regenerated, username)
	return a.mockAuthManager.RegenerateAPIKey(username)
}

func regenRequest(target, body string, asReseller bool) *http.Request {
	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	req.SetPathValue("username", target)
	if asReseller {
		return withResellerContext(req)
	}
	return withAdminContext(req)
}

func TestOwnAPIKeyRegenerationRequiresCurrentPassword(t *testing.T) {
	s := testServer()
	rec := &apiKeyRecorder{mockAuthManager: newMockAuthManager()}
	s.SetAuthManager(rec)

	// Hijacked admin session: no password at all, and an empty body.
	for _, body := range []string{``, `{}`, `{"current_password":""}`} {
		w := httptest.NewRecorder()
		s.handleUserRegenerateAPIKeyAuth(w, regenRequest("admin", body, false))
		t.Logf("EXPECTED: 400, key untouched\nACTUAL: %d, regenerated=%v", w.Code, rec.regenerated)
		if w.Code != http.StatusBadRequest || len(rec.regenerated) != 0 {
			t.Fatalf("PROBLEM CONFIRMED: own key replaced without current_password (body %q, status %d)", body, w.Code)
		}
	}

	// Wrong password is refused.
	w := httptest.NewRecorder()
	s.handleUserRegenerateAPIKeyAuth(w, regenRequest("admin", `{"current_password":"wrong"}`, false))
	if w.Code != http.StatusUnauthorized || len(rec.regenerated) != 0 {
		t.Fatalf("wrong password: status %d regenerated %v, want 401 and no change", w.Code, rec.regenerated)
	}

	// Malformed JSON is refused too.
	w = httptest.NewRecorder()
	s.handleUserRegenerateAPIKeyAuth(w, regenRequest("admin", `{not json`, false))
	if w.Code != http.StatusBadRequest || len(rec.regenerated) != 0 {
		t.Fatalf("malformed body: status %d regenerated %v, want 400 and no change", w.Code, rec.regenerated)
	}

	// The right password goes through.
	w = httptest.NewRecorder()
	s.handleUserRegenerateAPIKeyAuth(w, regenRequest("admin", `{"current_password":"password"}`, false))
	if w.Code != http.StatusOK || len(rec.regenerated) != 1 || rec.regenerated[0] != "admin" {
		t.Fatalf("correct password: status %d regenerated %v, want 200 and one regeneration", w.Code, rec.regenerated)
	}
}

func TestAdminRegeneratesOtherUsersAPIKeyWithoutPassword(t *testing.T) {
	s := testServer()
	rec := &apiKeyRecorder{mockAuthManager: newMockAuthManager()}
	s.SetAuthManager(rec)

	w := httptest.NewRecorder()
	s.handleUserRegenerateAPIKeyAuth(w, regenRequest("reseller", ``, false))
	if w.Code != http.StatusOK || len(rec.regenerated) != 1 || rec.regenerated[0] != "reseller" {
		t.Fatalf("admin replacing another user's key: status %d regenerated %v, want 200", w.Code, rec.regenerated)
	}
}

func TestNonAdminCannotRegenerateOthersAPIKey(t *testing.T) {
	s := testServer()
	rec := &apiKeyRecorder{mockAuthManager: newMockAuthManager()}
	s.SetAuthManager(rec)

	w := httptest.NewRecorder()
	s.handleUserRegenerateAPIKeyAuth(w, regenRequest("admin", `{"current_password":"password"}`, true))
	if w.Code != http.StatusForbidden || len(rec.regenerated) != 0 {
		t.Fatalf("reseller replacing admin's key: status %d regenerated %v, want 403", w.Code, rec.regenerated)
	}
}
