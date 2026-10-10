package admin

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/uwaserver/uwas/internal/auth"
)

// seedAuthUsers writes users.json with cheap (bcrypt.MinCost) hashes so these
// tests do not pay auth.BcryptCost (14) per login under the race detector.
func seedAuthUsers(t *testing.T, dir string, disabled map[string]bool, names ...string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var users []*auth.User
	for _, n := range names {
		users = append(users, &auth.User{ID: "id-" + n, Username: n, Password: string(hash),
			Role: auth.RoleUser, Enabled: !disabled[n], CreatedAt: time.Now()})
	}
	data, _ := json.Marshal(users)
	if err := os.WriteFile(filepath.Join(dir, "users.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// postLogin drives handleLogin and reports the status code (-1 plus the panic
// text if the handler panicked).
func postLogin(s *Server, user, pass string) (code int, panicked string) {
	defer func() {
		if r := recover(); r != nil {
			code, panicked = -1, fmt.Sprint(r)
		}
	}()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
	req.RemoteAddr = "10.9.8.7:4321"
	s.handleLogin(rec, req)
	return rec.Code, ""
}

func countAudit(s *Server, action string) int {
	n := 0
	for _, e := range s.auditBuf.Snapshot() {
		if e.Action == action {
			n++
		}
	}
	return n
}

// auth.Manager reports failed logins to the audit recorder with a nil request.
// With the production wiring (internal/server/server.go) the recorder is
// Server.RecordAuditR, which dereferenced the nil request and panicked inside
// AuthenticateFrom: no failed login was answered, audited, or counted by the
// per-IP limiter, so one IP could spray passwords across usernames (F1870).
func TestFailedLoginWithProductionAuditWiring(t *testing.T) {
	s := testServer()
	dir := t.TempDir()
	names := []string{"alice", "bob"}
	for i := 0; i < 12; i++ {
		names = append(names, fmt.Sprintf("spray%02d", i))
	}
	seedAuthUsers(t, dir, map[string]bool{"bob": true}, names...)
	mgr, err := auth.NewManager(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	mgr.SetAuditRecorder(s.RecordAuditR) // same wiring as internal/server/server.go
	s.SetAuthManager(mgr)

	// Control: a successful login is unaffected.
	if code, p := postLogin(s, "alice", "correct horse battery"); code != 200 {
		t.Fatalf("control login: code=%d panic=%q", code, p)
	}

	// Every failure kind answers 401 and is audited: wrong password, disabled
	// user, unknown user.
	failures := 0
	for _, user := range []string{"alice", "bob", "nosuchuser"} {
		code, p := postLogin(s, user, "guess")
		if code != 401 {
			t.Errorf("failed login for %q: code=%d panic=%q, want 401", user, code, p)
		}
		failures++
	}
	if got := countAudit(s, "auth.login.failed"); got != failures {
		t.Errorf("auth.login.failed audit entries = %d, want %d", got, failures)
	}

	// Password spraying: distinct usernames from one IP never trip the
	// per-(user,IP) manager lockout, so the handler's per-IP limiter must
	// count them and block the IP.
	for i := 0; i < 12; i++ {
		if code, p := postLogin(s, fmt.Sprintf("spray%02d", i), "guess"); code != 401 {
			t.Fatalf("spray %d: code=%d panic=%q, want 401", i, code, p)
		}
	}
	if !s.checkRateLimit("10.9.8.7", "") {
		t.Error("IP not blocked after 15 failed logins from it")
	}
}

// RecordAuditR is also the manager-facing entry point: it must accept a nil
// request and still record the entry.
func TestRecordAuditRNilRequest(t *testing.T) {
	s := testServer()
	s.RecordAuditR(nil, "auth.login.failed", "user=x: invalid credentials", false)
	if got := countAudit(s, "auth.login.failed"); got != 1 {
		t.Errorf("entries = %d, want 1", got)
	}
}
