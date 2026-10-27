package admin

// F31300/F31301: starting, stopping or restarting a system service (SSH,
// MariaDB, ufw, fail2ban...) left no audit record, unlike every other
// state-changing admin action, and an unknown service name answered 500
// "service ... failed" as if the server had broken.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/services"
)

func auditActions(s *Server) []string {
	var out []string
	if s.auditBuf == nil {
		return out
	}
	for _, e := range s.auditBuf.Snapshot() {
		out = append(out, fmt.Sprintf("%s|%v", e.Action, e.Success))
	}
	return out
}

func hasAudit(s *Server, want string) bool {
	for _, a := range auditActions(s) {
		if a == want {
			return true
		}
	}
	return false
}

func TestServiceActionsAreAudited(t *testing.T) {
	s := testServer()
	origStart, origStop, origRestart := servicesStartService, servicesStopService, servicesRestartService
	defer func() {
		servicesStartService, servicesStopService, servicesRestartService = origStart, origStop, origRestart
	}()
	servicesStartService = func(string) error { return nil }
	servicesStopService = func(string) error { return nil }
	servicesRestartService = func(string) error { return errors.New("boom") }

	for _, c := range []struct {
		verb string
		want string
	}{
		{"start", "service.start|true"},
		{"stop", "service.stop|true"},
		{"restart", "service.restart|false"}, // failures are audited too
	} {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/services/ssh/"+c.verb, nil))
		t.Logf("%s -> %d", c.verb, rec.Code)
		ok := hasAudit(s, c.want)
		t.Logf("EXPECTED: audit %q\nACTUAL: %v", c.want, auditActions(s))
		if !ok {
			t.Fatalf("PROBLEM CONFIRMED: %s left no audit record %q", c.verb, c.want)
		}
	}
}

func TestUnknownServiceIsNotFound(t *testing.T) {
	s := testServer()
	origStart := servicesStartService
	defer func() { servicesStartService = origStart }()
	servicesStartService = services.StartService // real allowlist check, rejects before any exec

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/services/not-a-service/start", nil))
	t.Logf("EXPECTED: 404\nACTUAL: %d %s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PROBLEM CONFIRMED: unknown service answered %d, want 404", rec.Code)
	}
}
