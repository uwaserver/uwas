package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestRecoveryCodesStoredAsBcryptHashes(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes", nil)
	w := httptest.NewRecorder()
	srv.handleGenRecoveryCodes(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Codes) != 8 {
		t.Fatalf("got %d codes, want 8", len(resp.Codes))
	}

	srv.configMu.RLock()
	stored := append([]string(nil), srv.config.Global.Admin.RecoveryCodes...)
	srv.configMu.RUnlock()
	if len(stored) != 8 {
		t.Fatalf("stored %d, want 8", len(stored))
	}

	// Every stored value must be a bcrypt hash, never the plaintext.
	plain := make(map[string]struct{}, len(resp.Codes))
	for _, c := range resp.Codes {
		plain[c] = struct{}{}
	}
	for i, s := range stored {
		if !recoveryCodeLooksHashed(s) {
			t.Errorf("stored[%d] is not a bcrypt hash: %q", i, s)
		}
		if _, ok := plain[s]; ok {
			t.Errorf("stored[%d] equals one of the plaintext codes — secrets leaked", i)
		}
	}

	// And each plaintext code must verify against exactly one hash.
	for _, code := range resp.Codes {
		matches := 0
		for _, h := range stored {
			if bcrypt.CompareHashAndPassword([]byte(h), []byte(code)) == nil {
				matches++
			}
		}
		if matches != 1 {
			t.Errorf("plaintext %q matched %d hashes, want 1", code, matches)
		}
	}
}

func TestRecoveryCodesResponseDisablesCaching(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes", nil)
	w := httptest.NewRecorder()
	srv.handleGenRecoveryCodes(w, req)

	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want substring no-store", cc)
	}
}

func TestUseRecoveryCodeAcceptsLegacyPlaintext(t *testing.T) {
	srv := testServer()

	// Seed the config with a mix of legacy plaintext codes and one
	// bcrypt-hashed code, the way a partial migration might look.
	const legacy1 = "deadbeef"
	const legacy2 = "feedface"
	const hashedPlain = "abcdef01"
	hash, err := bcrypt.GenerateFromPassword([]byte(hashedPlain), recoveryCodeBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	srv.configMu.Lock()
	srv.config.Global.Admin.RecoveryCodes = []string{legacy1, string(hash), legacy2}
	srv.configMu.Unlock()

	// Legacy plaintext should still verify.
	body := strings.NewReader(`{"code":"` + legacy1 + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes/use", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleUseRecoveryCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy verify status = %d, body = %s", w.Code, w.Body.String())
	}
	srv.configMu.RLock()
	if len(srv.config.Global.Admin.RecoveryCodes) != 2 {
		t.Errorf("legacy code not removed; remaining = %d", len(srv.config.Global.Admin.RecoveryCodes))
	}
	srv.configMu.RUnlock()

	// Hashed code should also still verify.
	body = strings.NewReader(`{"code":"` + hashedPlain + `"}`)
	req = httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes/use", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.handleUseRecoveryCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("hashed verify status = %d, body = %s", w.Code, w.Body.String())
	}

	// The same code must not verify a second time (single-use).
	body = strings.NewReader(`{"code":"` + legacy1 + `"}`)
	req = httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes/use", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.handleUseRecoveryCode(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("replay status = %d, want 401", w.Code)
	}
}

func TestUseRecoveryCodeRejectsBadLengthEarly(t *testing.T) {
	srv := testServer()
	// Make sure there is something to compare against so a 401 result
	// is unambiguously due to the length check rather than empty store.
	hash, _ := bcrypt.GenerateFromPassword([]byte("12345678"), recoveryCodeBcryptCost)
	srv.configMu.Lock()
	srv.config.Global.Admin.RecoveryCodes = []string{string(hash)}
	srv.configMu.Unlock()

	for _, code := range []string{"", "abc", strings.Repeat("a", 65)} {
		body := strings.NewReader(`{"code":"` + code + `"}`)
		req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes/use", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.handleUseRecoveryCode(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("code %q: status = %d, want 401", code, w.Code)
		}
	}
}
