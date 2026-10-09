package admin

import (
	"encoding/base32"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/auth"
)

// Regression tests for F477: handle2FAVerify must activate exactly the secret
// the submitted code was verified against, and only while 2FA is disabled.

func totpActivationCode(t *testing.T, secret string, stepOffset int64) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return generateCode(key, uint64(time.Now().Unix()/totpPeriod+stepOffset))
}

func totpActivationReq(method, path, body, user string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	u := &auth.User{ID: user + "-id", Username: user, Role: auth.RoleAdmin, Enabled: true}
	return r.WithContext(auth.WithUser(r.Context(), u))
}

func totpActivationSetup(t *testing.T, s *Server, user string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handle2FASetup(rec, totpActivationReq("POST", "/api/v1/auth/2fa/setup", "", user))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup(%s) = %d %s", user, rec.Code, rec.Body.String())
	}
	s.pendingTOTPMu.Lock()
	defer s.pendingTOTPMu.Unlock()
	return s.pendingTOTP[user]
}

func totpActivationVerify(s *Server, user, code string) int {
	rec := httptest.NewRecorder()
	s.handle2FAVerify(rec, totpActivationReq("POST", "/api/v1/auth/2fa/verify", `{"code":"`+code+`"}`, user))
	return rec.Code
}

func totpActiveSecret(s *Server) string {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config.Global.Admin.TOTPSecret
}

// A re-setup that lands between code validation and activation must not
// activate the new, never-verified secret.
func TestTOTPVerifyDoesNotActivateRestartedSetup(t *testing.T) {
	s := testServer()
	a := totpActivationSetup(t, s, "admin")
	code := totpActivationCode(t, a, 0)

	s.totpStepMu.Lock() // hold verify inside validateTOTPNoReplay
	done := make(chan int, 1)
	go func() { done <- totpActivationVerify(s, "admin", code) }()
	for i := 0; ; i++ {
		buf := make([]byte, 1<<20)
		if strings.Contains(string(buf[:runtime.Stack(buf, true)]), "validateTOTPNoReplay(") {
			break
		}
		if i > 20000 {
			s.totpStepMu.Unlock()
			t.Fatal("verify never reached validateTOTPNoReplay")
		}
		runtime.Gosched()
	}
	b := totpActivationSetup(t, s, "admin")
	s.totpStepMu.Unlock()

	if got := <-done; got != http.StatusConflict {
		t.Fatalf("verify status = %d, want 409", got)
	}
	if active := totpActiveSecret(s); active != "" {
		t.Fatalf("an unverified secret was activated (is restarted setup: %v)", active == b)
	}
	if got := totpActivationVerify(s, "admin", totpActivationCode(t, b, 1)); got != http.StatusOK || totpActiveSecret(s) != b {
		t.Fatalf("restarted setup not completable: status=%d", got)
	}
}

// A second admin's setup started before 2FA was enabled must not replace the
// active secret once the first admin has activated theirs.
func TestTOTPVerifyStalePendingDoesNotReplaceActive(t *testing.T) {
	s := testServer()
	a := totpActivationSetup(t, s, "admin1")
	b := totpActivationSetup(t, s, "admin2")

	if got := totpActivationVerify(s, "admin1", totpActivationCode(t, a, 0)); got != http.StatusOK {
		t.Fatalf("admin1 verify = %d, want 200", got)
	}
	if got := totpActivationVerify(s, "admin2", totpActivationCode(t, b, 1)); got != http.StatusConflict {
		t.Fatalf("admin2 verify = %d, want 409", got)
	}
	if totpActiveSecret(s) != a {
		t.Fatal("active secret was replaced by a stale pending setup")
	}
}
