package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

// F57600: an admin resetting the password of a user that does not exist gets
// the same 404 as the other user-management calls (F53302), not a generic 400.
func TestAdminPasswordResetMissingUserIs404(t *testing.T) {
	s := testServer()
	mgr, err := auth.NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuthManager(mgr)
	if _, err := mgr.CreateUser("real", "", "S3cure-Passw0rd!", auth.RoleUser, nil); err != nil {
		t.Fatal(err)
	}
	do := func(path string) int {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("POST", path,
			strings.NewReader(`{"new_password":"An0ther-Str0ng-Pass!"}`)))
		return rec.Code
	}
	if c := do("/api/v1/auth/users/ghost/password"); c != http.StatusNotFound {
		t.Errorf("reset password of missing user = %d, want 404", c)
	}
	// control: an existing user is reset
	if c := do("/api/v1/auth/users/real/password"); c != http.StatusOK {
		t.Errorf("reset password of existing user = %d, want 200", c)
	}
}
