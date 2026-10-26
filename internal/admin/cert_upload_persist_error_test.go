package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// An upload switches the domain to manual TLS in memory and then writes the
// configuration. A configuration that could not be written used to be ignored,
// so the operator was told "uploaded" and the domain went back to its old SSL
// mode on the next restart. The settings, cloudflare, php and bulk-import
// handlers already treat that as a failed request.
func TestCertUploadPersistFailureIsReported(t *testing.T) {
	cert, key := selfSignedPair(t, "site.test")
	body, _ := json.Marshal(map[string]string{"cert": cert, "key": key})

	run := func(t *testing.T, block bool) int {
		e := newHostConflictEnv(t, []config.Domain{{Host: "site.test", Type: "static"}})
		certUploadDir = t.TempDir()
		t.Cleanup(func() { certUploadDir = "/var/lib/uwas/certs" })
		if block {
			blockConfigPath(t, e.cfgPath)
		}
		return e.do(http.MethodPost, "/api/v1/certs/site.test/upload", string(body), auth.RoleAdmin)
	}

	t.Run("control: writable config", func(t *testing.T) {
		if code := run(t, false); code != http.StatusOK {
			t.Fatalf("upload = %d, want 200", code)
		}
	})
	t.Run("unwritable config", func(t *testing.T) {
		if code := run(t, true); code != http.StatusInternalServerError {
			t.Fatalf("EXPECTED: 500 when the configuration cannot be written\nACTUAL: %d", code)
		}
	})
}
