package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

// F53301/F53302: user management answers 404 for a missing user and 400 when
// the change would remove the last enabled admin; the guard is atomic in the
// manager, not only a handler pre-check.
func TestUserMgmtStatusMapping(t *testing.T) {
	s := testServer()
	mgr, err := auth.NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuthManager(mgr)
	if _, err := mgr.CreateUser("boss", "", "S3cure-Passw0rd!", auth.RoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string) int {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec.Code
	}
	if c := do("PUT", "/api/v1/auth/users/ghost", `{"email":"a@b.c"}`); c != http.StatusNotFound {
		t.Errorf("update ghost = %d, want 404", c)
	}
	if c := do("POST", "/api/v1/auth/users/ghost/apikey", `{}`); c != http.StatusNotFound {
		t.Errorf("apikey ghost = %d, want 404", c)
	}
	// testMux acts as another admin ("admin"), so disabling the only real
	// enabled admin must be refused with 400.
	if c := do("PUT", "/api/v1/auth/users/boss", `{"enabled":false}`); c != http.StatusBadRequest {
		t.Errorf("disable last admin = %d, want 400", c)
	}
}
