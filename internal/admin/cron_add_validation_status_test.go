package admin

// F33400: POST /api/v1/cron answered every rejected job with 500. A malformed
// schedule or a command with shell metacharacters is the caller's mistake, not a
// server fault; 5xx also trips client retry and alerting. The validation runs
// before crontab is touched, so these cases never reach the host.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCronAddValidationErrorsAre400(t *testing.T) {
	cases := []struct{ name, body string }{
		{"schedule word", `{"schedule":"daily","command":"echo hi"}`},
		{"schedule field count", `{"schedule":"* * * *","command":"echo hi"}`},
		{"schedule bad field", `{"schedule":"* * * * x$","command":"echo hi"}`},
		{"command newline", `{"schedule":"* * * * *","command":"echo hi\n* * * * * id"}`},
		{"command substitution", `{"schedule":"* * * * *","command":"echo $(id)"}`},
		{"command escaped percent", `{"schedule":"* * * * *","command":"date +\\%s"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testServer()
			rec := httptest.NewRecorder()
			s.handleCronAdd(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/cron", strings.NewReader(c.body))))
			t.Logf("EXPECTED: 400\nACTUAL: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PROBLEM CONFIRMED: validation error answered %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "error") {
				t.Fatalf("response carries no error message: %s", rec.Body.String())
			}
		})
	}
}

// Control: the pre-existing 400 for a missing field is unchanged.
func TestCronAddMissingFieldStill400(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleCronAdd(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/cron", strings.NewReader(`{"schedule":"* * * * *"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
