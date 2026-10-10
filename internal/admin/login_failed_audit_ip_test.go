package admin

import (
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

func failedLoginIPs(s *Server) []string {
	var ips []string
	for _, e := range s.auditBuf.Snapshot() {
		if e.Action == "auth.login.failed" {
			ips = append(ips, e.IP)
		}
	}
	return ips
}

// A failed login is audited with the client's source IP, like a successful
// one; it was recorded with a nil request and so always had an empty IP
// (F1900). The consent flag must still redact it.
func TestFailedLoginAuditCarriesClientIP(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recordIP bool
		want     string
	}{
		{"record_ip_on", true, "10.9.8.7"},
		{"record_ip_off_redacted", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer()
			s.SetAuditRecordIP(tc.recordIP)
			dir := t.TempDir()
			seedAuthUsers(t, dir, map[string]bool{"bob": true}, "alice", "bob")
			mgr, err := auth.NewManager(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(mgr.Stop)
			mgr.SetAuditRecorder(s.RecordAuditR)
			s.SetAuthManager(mgr)

			for _, user := range []string{"alice", "bob", "nosuchuser"} { // wrong pw, disabled, unknown
				if code, p := postLogin(s, user, "guess"); code != 401 {
					t.Fatalf("%s: code=%d panic=%q", user, code, p)
				}
			}
			ips := failedLoginIPs(s)
			if len(ips) != 3 {
				t.Fatalf("failed-login entries = %d, want 3", len(ips))
			}
			for _, ip := range ips {
				if ip != tc.want {
					t.Errorf("audit ip = %q, want %q", ip, tc.want)
				}
			}
		})
	}

	// The manager's request-less entry point still audits (no request, no IP).
	s := testServer()
	s.SetAuditRecordIP(true)
	dir := t.TempDir()
	seedAuthUsers(t, dir, nil, "alice")
	mgr, err := auth.NewManager(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	mgr.SetAuditRecorder(s.RecordAuditR)
	if _, err := mgr.AuthenticateFrom("alice", "guess", "10.1.1.1"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if ips := failedLoginIPs(s); len(ips) != 1 || ips[0] != "" {
		t.Errorf("request-less audit entries = %q, want one with empty ip", ips)
	}
}
