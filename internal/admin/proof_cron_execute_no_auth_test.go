package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestCronExecuteNoAuthContextProof verifies that handleCronExecute executes a
// command when called with a bare request context (no auth.User in context).
//
// ROOT CAUSE: handlers_cron.go lines 47-55:
//
//	if s.authMgr != nil {
//	    user, ok := auth.UserFromContext(r.Context())
//	    if ok && user.Role != auth.RoleAdmin {
//	        jsonError(w, "forbidden: admin only", http.StatusForbidden)
//	        return
//	    }
//	}
//
// The guard only returns 403 when authMgr != nil AND the context contains a
// non-admin user.  For a raw HTTP request arriving at the mux, r.Context()
// has no auth.User → UserFromContext returns (nil, false) → the entire
// if-block is skipped → command executes without authentication.
//
// In single-user mode (the default), authMgr is always nil, so this guard
// never fires.  The endpoint is accessible to any client reaching the admin
// port and can execute arbitrary shell commands as the UWAS process user.
//
// NOTE: We call handleCronExecute with context.Background() to reproduce the
// exact unauthenticated request path.  All existing tests use withAdminContext
// which is why this bug was not caught.
func TestCronExecuteNoAuthContextProof(t *testing.T) {
	log := logger.New("info", "text")
	s := New(&config.Config{}, log, nil)

	// Wire a real cron monitor so Execute() actually runs.
	monitor := cronjob.NewMonitor(t.TempDir())
	s.SetCronMonitor(monitor)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cron/execute",
		strings.NewReader(`{"domain":"test.com","command":"echo UNAUTH-EXEC","schedule":"* * * * *"}`))
	req.Header.Set("Content-Type", "application/json")

	// Call WITHOUT withAdminContext — this is a raw unauthenticated HTTP request.
	// All existing tests use withAdminContext which is why this bug is hidden.
	s.handleCronExecute(rec, req.WithContext(context.Background()))

	// The bug: handler returns 200 and processes the command.
	// Correct behavior: 401 or 403 (unauthenticated request rejected).
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Log("PASS: unauthenticated request correctly rejected")
		return
	}

	// The command executed (or at least the handler processed the request).
	// Status will be 200 with success=false if the domain dir doesn't exist,
	// but the handler DID process the request — it did not reject it as auth-required.
	t.Logf("status: %d, body: %s", rec.Code, rec.Body.String())

	t.Fatal("BUG CONFIRMED: handleCronExecute processed a request without any " +
		"auth context. auth.UserFromContext returned (nil, false) from the bare " +
		"context.Background(), so the admin-role check was skipped entirely. " +
		"The handler executed the command without verifying the caller is an admin.")
}
