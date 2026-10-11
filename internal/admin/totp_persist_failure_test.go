package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F2020: 2FA enable/disable must not report success when the change could not
// be persisted — it would revert (enable lost, or disable undone) on restart.

// blockConfigPath makes persistConfig fail by putting a non-empty directory
// where the main config file must be renamed to.
func blockConfigPath(t *testing.T, p string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func totpDisable(t *testing.T, s *Server, secret string, step int64) int {
	t.Helper()
	rec := httptest.NewRecorder()
	body := `{"code":"` + totpActivationCode(t, secret, step) + `"}`
	s.handle2FADisable(rec, totpActivationReq("POST", "/api/v1/auth/2fa/disable", body, "admin"))
	return rec.Code
}

func persistedHas(t *testing.T, p, needle string) bool {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

func TestTOTPEnablePersistFailureRollsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	s := testServer()
	s.SetConfigPath(p)
	secret := totpActivationSetup(t, s, "admin")

	blockConfigPath(t, p)
	if got := totpActivationVerify(s, "admin", totpActivationCode(t, secret, 0)); got != http.StatusInternalServerError {
		t.Fatalf("verify with unwritable config = %d, want 500", got)
	}
	if totpActiveSecret(s) != "" {
		t.Fatal("2FA stayed active in memory although it was not persisted")
	}

	// The setup stays pending: once the config is writable again the same
	// secret can be confirmed with a fresh code and survives a reload.
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if got := totpActivationVerify(s, "admin", totpActivationCode(t, secret, 1)); got != http.StatusOK {
		t.Fatalf("retry verify = %d, want 200", got)
	}
	if totpActiveSecret(s) != secret || !persistedHas(t, p, secret) {
		t.Fatal("retry did not activate and persist the secret")
	}
}

func TestTOTPDisablePersistFailureKeepsEnabled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	s := testServer()
	s.SetConfigPath(p)
	secret := totpActivationSetup(t, s, "admin")
	if got := totpActivationVerify(s, "admin", totpActivationCode(t, secret, 0)); got != http.StatusOK {
		t.Fatalf("enable = %d", got)
	}

	blockConfigPath(t, p)
	if got := totpDisable(t, s, secret, 1); got != http.StatusInternalServerError {
		t.Fatalf("disable with unwritable config = %d, want 500", got)
	}
	if totpActiveSecret(s) != secret {
		t.Fatal("2FA was turned off in memory although the change was not persisted")
	}

	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	// The failed attempt consumed its time step; a real retry would use the
	// next period's code, so forget the consumed step instead of waiting 30s.
	s.totpStepMu.Lock()
	s.lastTOTPStep = 0
	s.totpStepMu.Unlock()
	if got := totpDisable(t, s, secret, 0); got != http.StatusOK {
		t.Fatalf("retry disable = %d, want 200", got)
	}
	if totpActiveSecret(s) != "" || persistedHas(t, p, secret) {
		t.Fatal("retry did not disable and persist")
	}
}
