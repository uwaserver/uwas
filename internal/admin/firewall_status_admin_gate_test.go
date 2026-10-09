package admin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"golang.org/x/crypto/ssh"
)

// fakeUFWOnPath puts a recording ufw stub first (and only) on PATH so the
// firewall package's heal never reaches the real binary.
func fakeUFWOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "ufw.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"case \"$1\" in status) printf 'Status: active\\n\\n[ 1] 22/tcp                     ALLOW IN    Anywhere\\n';; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "ufw"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return log
}

// TestFirewallStatusRequiresAdmin: GET /api/v1/firewall lists host-wide rules
// and heals the default deny by running ufw, so non-admins must get 403
// before any ufw call.
func TestFirewallStatusRequiresAdmin(t *testing.T) {
	log := fakeUFWOnPath(t)
	s := testServer()
	for name, wrap := range map[string]func(*http.Request) *http.Request{
		"user": withUserContext, "reseller": withResellerContext,
	} {
		_ = os.Remove(log)
		rec := httptest.NewRecorder()
		s.handleFirewallStatus(rec, wrap(httptest.NewRequest(http.MethodGet, "/api/v1/firewall", nil)))
		calls, _ := os.ReadFile(log)
		if rec.Code != http.StatusForbidden || len(calls) != 0 {
			t.Errorf("%s: status=%d ufw calls=%q, want 403 and no calls", name, rec.Code, calls)
		}
	}
	rec := httptest.NewRecorder()
	s.handleFirewallStatus(rec, withAdminContext(httptest.NewRequest(http.MethodGet, "/api/v1/firewall", nil)))
	if rec.Code != http.StatusOK {
		t.Errorf("admin: status=%d, want 200", rec.Code)
	}
}

// TestSSHKeyAddAcceptsECDSA: ecdsa-sha2-* is a valid OpenSSH key type that
// does not start with "ssh-".
func TestSSHKeyAddAcceptsECDSA(t *testing.T) {
	s := testServer()
	base := t.TempDir()
	root := filepath.Join(base, "keys.example", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s.configMu.Lock()
	s.config.Domains = append(s.config.Domains, config.Domain{Host: "keys.example", Type: "static", Root: root})
	s.configMu.Unlock()

	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub, _ := ssh.NewPublicKey(&ec.PublicKey)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	body, _ := json.Marshal(map[string]string{"public_key": line})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/keys.example/ssh-keys", strings.NewReader(string(body)))
	req.SetPathValue("domain", "keys.example")
	rec := httptest.NewRecorder()
	s.handleSSHKeyAdd(rec, withAdminContext(req))
	data, _ := os.ReadFile(filepath.Join(base, "keys.example", ".ssh", "authorized_keys"))
	if rec.Code != http.StatusOK || !strings.Contains(string(data), line) {
		t.Fatalf("status=%d stored=%v, want 200 and stored", rec.Code, strings.Contains(string(data), line))
	}
}
