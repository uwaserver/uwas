package admin

// F36000: with 2FA enabled, the per-request gate in authMiddleware validated
// X-TOTP-Code through validateTOTPNoReplay, which burns the matched time step.
// api.go documents that burning is for the one-time enable/disable/step-up
// operations and "is NOT applied to the per-request 2FA gate, where the same
// code is legitimately presented on every request inside its 30s window" - and
// that is exactly what the dashboard does (it keeps the code it logged in with
// and sends it on every call). The second request in a step was therefore
// rejected with "invalid 2FA code", and every rejection fed the per-IP failure
// counter, so the operator was locked out within seconds of logging in.

import (
	"encoding/base32"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

const totpGateKey = "adminkey-0123456789"

func totpGateServer(t *testing.T) (http.Handler, *Server, string) {
	t.Helper()
	cfg := &config.Config{Global: config.GlobalConfig{WebRoot: t.TempDir(), LogLevel: "info", LogFormat: "text"}}
	cfg.Global.Admin.APIKey = totpGateKey
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Global.Admin.TOTPSecret = secret
	s := testServerFromConfig(t, cfg)
	return s.authMiddleware(requireJSONMiddleware(s.mux.(*testMux).mux)), s, secret
}

func totpGateCode(t *testing.T, secret string) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return generateCode(key, uint64(time.Now().Unix()/totpPeriod))
}

func totpGateGET(h http.Handler, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "http://admin.local/api/v1/system", nil)
	req.RemoteAddr = "10.1.1.1:5555"
	req.Header.Set("Authorization", "Bearer "+totpGateKey)
	if code != "" {
		req.Header.Set("X-TOTP-Code", code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The same valid code, presented on every request, keeps working for as long as
// it is valid, and never feeds the lockout counter.
func TestTOTPGateAcceptsSameCodeOnEveryRequest(t *testing.T) {
	h, _, secret := totpGateServer(t)
	code := totpGateCode(t, secret)
	for i := 1; i <= 25; i++ {
		rec := totpGateGET(h, code)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d with the same valid TOTP code = %d %s, want 200", i, rec.Code, rec.Body.String())
		}
	}
}

// Controls: the gate still rejects a missing and a wrong code.
func TestTOTPGateStillRejectsMissingAndWrongCode(t *testing.T) {
	h, _, secret := totpGateServer(t)
	if rec := totpGateGET(h, ""); rec.Code != http.StatusForbidden || rec.Header().Get("X-2FA-Required") != "true" {
		t.Fatalf("missing code = %d (X-2FA-Required %q), want 403 + header", rec.Code, rec.Header().Get("X-2FA-Required"))
	}
	good := totpGateCode(t, secret)
	wrong := "000000"
	if good == wrong {
		wrong = "000001"
	}
	if rec := totpGateGET(h, wrong); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong code = %d, want 403", rec.Code)
	}
	if rec := totpGateGET(h, good); rec.Code != http.StatusOK {
		t.Fatalf("good code after one wrong attempt = %d, want 200", rec.Code)
	}
}

// The gate must not consume the step: one-time operations (2FA enable/disable,
// recovery-code step-up) still burn it, exactly once.
func TestTOTPGateDoesNotBurnStepForOneTimeOperations(t *testing.T) {
	h, s, secret := totpGateServer(t)
	code := totpGateCode(t, secret)
	if rec := totpGateGET(h, code); rec.Code != http.StatusOK {
		t.Fatalf("gate request = %d, want 200", rec.Code)
	}
	if !s.validateTOTPNoReplay(secret, code) {
		t.Fatal("a one-time operation could not use the code after the gate saw it")
	}
	if s.validateTOTPNoReplay(secret, code) {
		t.Fatal("a one-time operation accepted the same code twice")
	}
}
