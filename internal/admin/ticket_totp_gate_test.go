package admin

// F37000: SSE (EventSource) and WebSocket clients cannot set request headers,
// so the dashboard authenticates them with a short-lived, single-use ticket in
// the query string (POST /auth/ticket, minted by a header-authenticated call).
// The ticket carries the PIN the mint request proved, but not the second factor:
// the 2FA gate in authMiddleware still demanded X-TOTP-Code on the redemption
// request, which a browser cannot send. With 2FA enabled the live stats stream
// and the web terminal were therefore unreachable. The mint request itself
// passed the gate, so the ticket now carries that fact too.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

func totpTicketDo(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://admin.local"+path, nil)
	req.RemoteAddr = "10.1.1.1:5555"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func totpMintTicket(t *testing.T, h http.Handler, code string) string {
	t.Helper()
	hdr := map[string]string{"Authorization": "Bearer " + totpGateKey, "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json"}
	if code != "" {
		hdr["X-TOTP-Code"] = code
	}
	rec := totpTicketDo(h, "POST", "/api/v1/auth/ticket", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint ticket = %d %s", rec.Code, rec.Body.String())
	}
	var resp TicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Ticket == "" {
		t.Fatalf("mint response %q: %v", rec.Body.String(), err)
	}
	return resp.Ticket
}

// A ticket minted by a request that passed the 2FA gate is redeemable without
// the header; it stays single-use.
func TestTicketMintedUnder2FARedeemsWithoutTOTPHeader(t *testing.T) {
	h, _, secret := totpGateServer(t)
	ticket := totpMintTicket(t, h, totpGateCode(t, secret))

	rec := totpTicketDo(h, "GET", "/api/v1/system?ticket="+ticket, nil)
	t.Logf("EXPECTED: 200\nACTUAL: %d %s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("PROBLEM CONFIRMED: ticket redemption under 2FA = %d, want 200", rec.Code)
	}

	// Single use: the same ticket does not authenticate twice.
	if rec := totpTicketDo(h, "GET", "/api/v1/system?ticket="+ticket, nil); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("replayed ticket = %d, want 401/403", rec.Code)
	}
}

// Controls: the header still gates everything a ticket does not vouch for.
func TestTicketDoesNotWeaken2FAElsewhere(t *testing.T) {
	h, _, secret := totpGateServer(t)

	// No ticket, no header: still 2fa_required.
	if rec := totpTicketDo(h, "GET", "/api/v1/system", map[string]string{"Authorization": "Bearer " + totpGateKey}); rec.Code != http.StatusForbidden {
		t.Fatalf("header-less request = %d, want 403", rec.Code)
	}

	// A wrong code presented alongside a ticket is still rejected.
	ticket := totpMintTicket(t, h, totpGateCode(t, secret))
	rec := totpTicketDo(h, "GET", "/api/v1/system?ticket="+ticket, map[string]string{"X-TOTP-Code": "000000"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ticket + wrong TOTP code = %d, want 403", rec.Code)
	}

	// A ticket cannot be minted without the code in the first place.
	hdr := map[string]string{"Authorization": "Bearer " + totpGateKey, "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json"}
	if rec := totpTicketDo(h, "POST", "/api/v1/auth/ticket", hdr); rec.Code != http.StatusForbidden {
		t.Fatalf("mint without TOTP code = %d, want 403", rec.Code)
	}
}

// A ticket minted while 2FA was off must not carry a pass into a later 2FA
// enrolment (and one bound to an old secret must not survive a re-enrolment).
func TestTicketTOTPBindingFollowsTheSecret(t *testing.T) {
	h, s, secret := totpGateServer(t)

	// Mint under secret A, then the secret changes before redemption.
	ticket := totpMintTicket(t, h, totpGateCode(t, secret))
	s.configMu.Lock()
	other, err := GenerateTOTPSecret()
	if err != nil {
		s.configMu.Unlock()
		t.Fatal(err)
	}
	s.config.Global.Admin.TOTPSecret = other
	s.configMu.Unlock()
	if rec := totpTicketDo(h, "GET", "/api/v1/system?ticket="+ticket, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("ticket minted under an old secret = %d, want 403", rec.Code)
	}

	// Mint with 2FA off, enrol afterwards.
	s.configMu.Lock()
	s.config.Global.Admin.TOTPSecret = ""
	s.configMu.Unlock()
	ticket = totpMintTicket(t, h, "")
	s.configMu.Lock()
	s.config.Global.Admin.TOTPSecret = secret
	s.configMu.Unlock()
	if rec := totpTicketDo(h, "GET", "/api/v1/system?ticket="+ticket, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("ticket minted before 2FA was enabled = %d, want 403", rec.Code)
	}
}

// The multi-user session path: an admin's browser session mints the ticket with
// X-Session-Token and redeems it on the stream without any header.
func TestTicketMintedUnder2FARedeemsForAdminSession(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Global: config.GlobalConfig{WebRoot: dir, LogLevel: "info", LogFormat: "text"}}
	cfg.Global.Admin.APIKey = totpGateKey
	cfg.Global.Users.Enabled = true
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Global.Admin.TOTPSecret = secret
	s := testServerFromConfig(t, cfg)

	hash, _ := bcrypt.GenerateFromPassword([]byte("pw-correct-horse"), bcrypt.MinCost)
	users := []*auth.User{{ID: "id-boss", Username: "boss", Password: string(hash), Role: auth.RoleAdmin, Enabled: true, CreatedAt: time.Now()}}
	adir := filepath.Join(dir, "auth")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(users)
	if err := os.WriteFile(filepath.Join(adir, "users.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := auth.NewManager(adir, totpGateKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	s.SetAuthManager(mgr)
	h := s.authMiddleware(requireJSONMiddleware(s.mux.(*testMux).mux))

	sess, err := mgr.Authenticate("boss", "pw-correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	rec := totpTicketDo(h, "POST", "/api/v1/auth/ticket", map[string]string{
		"X-Session-Token": sess.Token, "X-TOTP-Code": totpGateCode(t, secret),
		"X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint = %d %s", rec.Code, rec.Body.String())
	}
	var resp TicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if rec := totpTicketDo(h, "GET", "/api/v1/system?ticket="+resp.Ticket, nil); rec.Code != http.StatusOK {
		t.Fatalf("session ticket redemption under 2FA = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// The web terminal is the stream this matters for most: the dashboard opens it
// as a WebSocket with the ticket in the URL. The request must get past the 2FA
// gate to the terminal handler itself (which is not reachable as a plain
// recorder, so only the gate's answer is asserted).
func TestTerminalTicketPassesTheTOTPGate(t *testing.T) {
	h, _, secret := totpGateServer(t)
	ticket := totpMintTicket(t, h, totpGateCode(t, secret))
	rec := totpTicketDo(h, "GET", "/api/v1/terminal?ticket="+ticket, map[string]string{
		"Upgrade": "websocket", "Connection": "Upgrade", "Sec-WebSocket-Version": "13",
		"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Origin": "http://admin.local",
	})
	t.Logf("terminal with ticket under 2FA: %d %s", rec.Code, rec.Body.String())
	if rec.Header().Get("X-2FA-Required") != "" || rec.Body.String() == `{"error":"2fa_required"}`+"\n" {
		t.Fatalf("terminal ticket was stopped by the 2FA gate: %d %s", rec.Code, rec.Body.String())
	}
}
