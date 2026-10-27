package admin

import (
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// Domain add, update and delete mutate the live configuration and then save it
// through notifyDomainChange, which only logged a failed save. The panel was
// told 201/200 for a change that disappeared on the next restart. The webhook,
// settings, cloudflare, php and cert-upload handlers already answer 500 for it.
func TestDomainCRUDReportsPersistFailure(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		ok                       int
	}{
		{"add", http.MethodPost, "/api/v1/domains", `{"host":"new.example.com","type":"static","ssl":{"mode":"off"}}`, http.StatusCreated},
		{"update", http.MethodPut, "/api/v1/domains/example.com", `{"cache":{"enabled":true}}`, http.StatusOK},
		{"delete", http.MethodDelete, "/api/v1/domains/example.com?confirm=true", ``, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name+"/control", func(t *testing.T) {
			e := newHostConflictEnv(t, []config.Domain{{Host: "example.com", Type: "static"}, {Host: "other.example.com", Type: "static"}})
			if code := e.do(c.method, c.path, c.body, auth.RoleAdmin); code != c.ok {
				t.Fatalf("%s = %d, want %d", c.name, code, c.ok)
			}
		})
		t.Run(c.name+"/unwritable config", func(t *testing.T) {
			e := newHostConflictEnv(t, []config.Domain{{Host: "example.com", Type: "static"}, {Host: "other.example.com", Type: "static"}})
			blockConfigPath(t, e.cfgPath)
			if code := e.do(c.method, c.path, c.body, auth.RoleAdmin); code != http.StatusInternalServerError {
				t.Fatalf("EXPECTED: 500 when the configuration cannot be written\nACTUAL: %d", code)
			}
		})
	}
}
