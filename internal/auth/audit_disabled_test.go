package auth

import (
	"net/http"
	"testing"
)

// TestAuditDisabledUserLoginMissingAudit verifies that a login attempt for a
// disabled user is recorded in the audit trail.  It reproduces the missing-audit
// defect in AuthenticateFrom's !enabled branch.
func TestAuditDisabledUserLoginMissingAudit(t *testing.T) {
	m := newTestManager(t)
	m.CreateUser("alice", "", "secret", RoleAdmin, nil)

	// Disable the user
	m.mu.Lock()
	m.users["alice"].Enabled = false
	m.mu.Unlock()

	var auditCalls []string
	m.SetAuditRecorder(func(r *http.Request, action, detail string, success bool) {
		auditCalls = append(auditCalls, action)
	})

	_, err := m.Authenticate("alice", "secret")
	if err == nil {
		t.Fatal("expected error for disabled user")
	}
	if g := err.Error(); g != "user disabled" {
		t.Fatalf("expected 'user disabled', got %q", g)
	}

	// Assert the failed attempt appears in the audit trail
	for _, action := range auditCalls {
		if action == "auth.login.failed" {
			return
		}
	}
	t.Errorf("audit log missing for disabled-user login attempt. recorded actions: %v", auditCalls)
}
