package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// errGrpA is a generic error used to drive handler error branches.
var errGrpA = errors.New("grpA injected error")

// A real, well-formed ed25519 SSH public key. siteuser.AddSSHKeyForWebDir
// parses the key with golang.org/x/crypto/ssh, so the prefix-only check in the
// handler is not enough — the key must actually parse for the 200 path.
const grpAValidSSHKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOxkr6PMmJ+UDkNyFbCLlgj0Cft2eTug9DV0/M2voWqv grpA@test"

// grpAReq builds a request with the given method/target and optional JSON body.
func grpAReq(method, target string, body any) *http.Request {
	if body != nil {
		b, _ := json.Marshal(body)
		return httptest.NewRequest(method, target, strings.NewReader(string(b)))
	}
	return httptest.NewRequest(method, target, nil)
}

func grpADecode(rec *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		return nil // body may be an array, not an object
	}
	return m
}

// =============================================================================
// Firewall: allow / deny
// =============================================================================

func TestGrpA_FirewallAllow(t *testing.T) {
	t.Run("no_context_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleFirewallAllow(rec, grpAReq("POST", "/x", map[string]string{"port": "80"}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleFirewallAllow(rec, withResellerContext(grpAReq("POST", "/x", map[string]string{"port": "80"})))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", strings.NewReader("{bad"))
		s.handleFirewallAllow(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing_port", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleFirewallAllow(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{"proto": "tcp"})))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		s := testServer()
		var gotPort, gotProto string
		orig := firewallAllowPort
		firewallAllowPort = func(port, proto string) error { gotPort, gotProto = port, proto; return nil }
		defer func() { firewallAllowPort = orig }()
		rec := httptest.NewRecorder()
		s.handleFirewallAllow(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{"port": "8080", "proto": "tcp"})))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if gotPort != "8080" || gotProto != "tcp" {
			t.Fatalf("seam got port=%q proto=%q", gotPort, gotProto)
		}
		if m := grpADecode(rec); m["status"] != "allowed" {
			t.Fatalf("status field = %v", m["status"])
		}
	})

	t.Run("seam_error", func(t *testing.T) {
		s := testServer()
		orig := firewallAllowPort
		firewallAllowPort = func(port, proto string) error { return errGrpA }
		defer func() { firewallAllowPort = orig }()
		rec := httptest.NewRecorder()
		s.handleFirewallAllow(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{"port": "80"})))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestGrpA_FirewallDeny(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleFirewallDeny(rec, withResellerContext(grpAReq("POST", "/x", map[string]string{"port": "80"})))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", strings.NewReader("nope"))
		s.handleFirewallDeny(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing_port", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleFirewallDeny(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{})))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		s := testServer()
		orig := firewallDenyPort
		firewallDenyPort = func(port, proto string) error { return nil }
		defer func() { firewallDenyPort = orig }()
		rec := httptest.NewRecorder()
		s.handleFirewallDeny(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{"port": "25"})))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("seam_error", func(t *testing.T) {
		s := testServer()
		orig := firewallDenyPort
		firewallDenyPort = func(port, proto string) error { return errGrpA }
		defer func() { firewallDenyPort = orig }()
		rec := httptest.NewRecorder()
		s.handleFirewallDeny(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{"port": "25"})))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// =============================================================================
// Firewall: status / delete / enable / disable
// =============================================================================

func TestGrpA_FirewallStatus(t *testing.T) {
	// firewallGetStatus is stubbed by TestMain to return an empty Status.
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleFirewallStatus(rec, grpAReq("GET", "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestGrpA_FirewallDelete(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", nil)
		req.SetPathValue("number", "2")
		s.handleFirewallDelete(rec, withResellerContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("invalid_number", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", nil)
		req.SetPathValue("number", "0")
		s.handleFirewallDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		s := testServer()
		orig := firewallDeleteRule
		var gotNum int
		firewallDeleteRule = func(n int) error { gotNum = n; return nil }
		defer func() { firewallDeleteRule = orig }()
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", nil)
		req.SetPathValue("number", "3")
		s.handleFirewallDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if gotNum != 3 {
			t.Fatalf("delete rule num = %d, want 3", gotNum)
		}
	})

	t.Run("seam_error", func(t *testing.T) {
		s := testServer()
		orig := firewallDeleteRule
		firewallDeleteRule = func(n int) error { return errGrpA }
		defer func() { firewallDeleteRule = orig }()
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", nil)
		req.SetPathValue("number", "5")
		s.handleFirewallDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestGrpA_FirewallEnable(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleFirewallEnable(rec, withResellerContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		s := testServer()
		orig := firewallEnable
		firewallEnable = func() error { return nil }
		defer func() { firewallEnable = orig }()
		rec := httptest.NewRecorder()
		s.handleFirewallEnable(rec, withAdminContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("seam_error", func(t *testing.T) {
		s := testServer()
		orig := firewallEnable
		firewallEnable = func() error { return errGrpA }
		defer func() { firewallEnable = orig }()
		rec := httptest.NewRecorder()
		s.handleFirewallEnable(rec, withAdminContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestGrpA_FirewallDisable(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleFirewallDisable(rec, withResellerContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("pin_required", func(t *testing.T) {
		s := testServer()
		s.config.Global.Admin.PinCode = "1234"
		rec := httptest.NewRecorder()
		s.handleFirewallDisable(rec, withAdminContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (pin)", rec.Code)
		}
	})

	t.Run("success_with_pin", func(t *testing.T) {
		s := testServer()
		s.config.Global.Admin.PinCode = "1234"
		orig := firewallDisable
		firewallDisable = func() error { return nil }
		defer func() { firewallDisable = orig }()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", nil)
		req.Header.Set("X-Pin-Code", "1234")
		s.handleFirewallDisable(rec, withAdminContext(req))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("seam_error", func(t *testing.T) {
		s := testServer()
		orig := firewallDisable
		firewallDisable = func() error { return errGrpA }
		defer func() { firewallDisable = orig }()
		rec := httptest.NewRecorder()
		s.handleFirewallDisable(rec, withAdminContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// =============================================================================
// SSH Keys (need a real domain root)
// =============================================================================

func TestGrpA_SSHKeyList(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := grpAReq("GET", "/x", nil)
	req.SetPathValue("domain", "example.com")
	s.handleSSHKeyList(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var arr []string
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("expected JSON array: %v (body=%s)", err, rec.Body.String())
	}
}

func TestGrpA_SSHKeyAdd(t *testing.T) {
	t.Run("invalid_json", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", strings.NewReader("{"))
		req.SetPathValue("domain", "example.com")
		s.handleSSHKeyAdd(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("bad_prefix", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", map[string]string{"public_key": "not-a-key"})
		req.SetPathValue("domain", "example.com")
		s.handleSSHKeyAdd(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("malformed_key_500", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		rec := httptest.NewRecorder()
		// Passes the "ssh-" prefix check but fails to parse → 500.
		req := grpAReq("POST", "/x", map[string]string{"public_key": "ssh-rsa not-valid-base64"})
		req.SetPathValue("domain", "example.com")
		s.handleSSHKeyAdd(rec, withAdminContext(req))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", map[string]string{"public_key": grpAValidSSHKey})
		req.SetPathValue("domain", "example.com")
		s.handleSSHKeyAdd(rec, withAdminContext(req))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		// Now it should appear in the list.
		recL := httptest.NewRecorder()
		reqL := grpAReq("GET", "/x", nil)
		reqL.SetPathValue("domain", "example.com")
		s.handleSSHKeyList(recL, withAdminContext(reqL))
		var arr []string
		json.Unmarshal(recL.Body.Bytes(), &arr)
		if len(arr) == 0 {
			t.Fatalf("expected key in list, got %v", arr)
		}
	})
}

func TestGrpA_SSHKeyDelete(t *testing.T) {
	t.Run("invalid_json", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("DELETE", "/x", strings.NewReader("{"))
		req.SetPathValue("domain", "example.com")
		s.handleSSHKeyDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("pin_required", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		s.config.Global.Admin.PinCode = "9999"
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", map[string]string{"fingerprint": "abc"})
		req.SetPathValue("domain", "example.com")
		s.handleSSHKeyDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (pin)", rec.Code)
		}
	})

	t.Run("success_no_keys_file", func(t *testing.T) {
		// RemoveSSHKeyForWebDir returns nil when no authorized_keys exists.
		s, _ := testServerWithRoot(t)
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", map[string]string{"fingerprint": grpAValidSSHKey})
		req.SetPathValue("domain", "example.com")
		s.handleSSHKeyDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

// =============================================================================
// DNS handlers
// =============================================================================

func TestGrpA_DNSCheck_BadRequest(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := grpAReq("GET", "/x", nil)
	req.SetPathValue("domain", "")
	s.handleDNSCheck(rec, withAdminContext(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGrpA_GetDNSProvider(t *testing.T) {
	// Each case sets ACME provider + credentials and asserts whether a provider
	// is constructed. No network is touched — only client construction.
	cases := []struct {
		name     string
		provider string
		creds    map[string]string
		wantNil  bool
	}{
		{"nil_creds", "cloudflare", nil, true},
		{"cloudflare_ok", "cloudflare", map[string]string{"api_token": "tok"}, false},
		{"cloudflare_token_alt", "cloudflare", map[string]string{"token": "tok"}, false},
		{"cloudflare_empty_token", "cloudflare", map[string]string{"other": "x"}, true},
		{"hetzner_ok", "hetzner", map[string]string{"api_token": "tok"}, false},
		{"hetzner_empty", "hetzner", map[string]string{"x": "y"}, true},
		{"digitalocean_ok", "digitalocean", map[string]string{"token": "tok"}, false},
		{"route53_ok", "route53", map[string]string{"access_key": "a", "secret_key": "b"}, false},
		{"route53_default_region", "route53", map[string]string{"access_key": "a", "secret_key": "b", "region": ""}, false},
		{"route53_missing_secret", "route53", map[string]string{"access_key": "a"}, true},
		{"unknown_provider", "weirddns", map[string]string{"api_token": "tok"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer()
			s.config.Global.ACME.DNSProvider = tc.provider
			s.config.Global.ACME.DNSCredentials = tc.creds
			got := s.getDNSProvider()
			if (got == nil) != tc.wantNil {
				t.Fatalf("getDNSProvider() nil=%v, want nil=%v", got == nil, tc.wantNil)
			}
		})
	}
}

func TestGrpA_DNSRecords_NotConfigured(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := grpAReq("GET", "/x", nil)
	req.SetPathValue("domain", "example.com")
	s.handleDNSRecords(rec, withAdminContext(req))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestGrpA_DNSRecordCreate(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", map[string]string{"type": "A"})
		req.SetPathValue("domain", "example.com")
		s.handleDNSRecordCreate(rec, withResellerContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("not_configured", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", map[string]string{"type": "A"})
		req.SetPathValue("domain", "example.com")
		s.handleDNSRecordCreate(rec, withAdminContext(req))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

func TestGrpA_DNSRecordDelete(t *testing.T) {
	t.Run("pin_required", func(t *testing.T) {
		s := testServer()
		s.config.Global.Admin.PinCode = "1111"
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", nil)
		req.SetPathValue("domain", "example.com")
		req.SetPathValue("id", "rec1")
		s.handleDNSRecordDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (pin)", rec.Code)
		}
	})

	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", nil)
		req.SetPathValue("domain", "example.com")
		req.SetPathValue("id", "rec1")
		s.handleDNSRecordDelete(rec, withResellerContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("not_configured", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("DELETE", "/x", nil)
		req.SetPathValue("domain", "example.com")
		req.SetPathValue("id", "rec1")
		s.handleDNSRecordDelete(rec, withAdminContext(req))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

func TestGrpA_DNSRecordUpdate(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		rec := httptest.NewRecorder()
		req := grpAReq("PUT", "/x", map[string]string{"type": "A"})
		req.SetPathValue("domain", "example.com")
		req.SetPathValue("id", "rec1")
		s.handleDNSRecordUpdate(rec, withResellerContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("not_configured", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("PUT", "/x", map[string]string{"type": "A"})
		req.SetPathValue("domain", "example.com")
		req.SetPathValue("id", "rec1")
		s.handleDNSRecordUpdate(rec, withAdminContext(req))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

func TestGrpA_DNSSync(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", nil)
		req.SetPathValue("domain", "example.com")
		s.handleDNSSync(rec, withResellerContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("not_configured", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", nil)
		req.SetPathValue("domain", "example.com")
		s.handleDNSSync(rec, withAdminContext(req))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

// =============================================================================
// System handlers
// =============================================================================

func TestGrpA_Update(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleUpdate(rec, withResellerContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("pin_required", func(t *testing.T) {
		s := testServer()
		s.config.Global.Admin.PinCode = "2222"
		rec := httptest.NewRecorder()
		s.handleUpdate(rec, withAdminContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (pin)", rec.Code)
		}
	})
}

func TestGrpA_DetectDockerComposePackage(t *testing.T) {
	// With the TestMain seam pointing systemExecCommand at `true` (exit 0, no
	// output), "docker compose version" succeeds → reports installed.
	installed, _ := detectDockerComposePackage()
	if !installed {
		t.Fatalf("detectDockerComposePackage installed = false, want true with no-op exec seam")
	}

	// When every exec'd command fails, it reports not installed.
	restore := setSystemExecCommand(func(name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	})
	defer restore()
	if inst, _ := detectDockerComposePackage(); inst {
		t.Fatalf("detectDockerComposePackage installed = true, want false when exec fails")
	}
}

func TestGrpA_ServiceControl(t *testing.T) {
	t.Run("start_reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", nil)
		req.SetPathValue("name", "nginx")
		s.handleServiceStart(rec, withResellerContext(req))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("start_success", func(t *testing.T) {
		s := testServer()
		orig := servicesStartService
		var got string
		servicesStartService = func(name string) error { got = name; return nil }
		defer func() { servicesStartService = orig }()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", nil)
		req.SetPathValue("name", "ssh")
		s.handleServiceStart(rec, withAdminContext(req))
		if rec.Code != http.StatusOK || got != "ssh" {
			t.Fatalf("status=%d got=%q", rec.Code, got)
		}
	})

	t.Run("stop_error", func(t *testing.T) {
		s := testServer()
		orig := servicesStopService
		servicesStopService = func(name string) error { return errGrpA }
		defer func() { servicesStopService = orig }()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", nil)
		req.SetPathValue("name", "ssh")
		s.handleServiceStop(rec, withAdminContext(req))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("restart_success", func(t *testing.T) {
		s := testServer()
		orig := servicesRestartService
		servicesRestartService = func(name string) error { return nil }
		defer func() { servicesRestartService = orig }()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", nil)
		req.SetPathValue("name", "ssh")
		s.handleServiceRestart(rec, withAdminContext(req))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("list_reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleServicesList(rec, withResellerContext(grpAReq("GET", "/x", nil)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
}

// =============================================================================
// Settings / Notify handlers
// =============================================================================

func TestGrpA_NotifyTest(t *testing.T) {
	t.Run("reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleNotifyTest(rec, withResellerContext(grpAReq("POST", "/x", map[string]string{"type": "slack"})))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", strings.NewReader("{bad"))
		s.handleNotifyTest(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("send_failure", func(t *testing.T) {
		// An unknown/empty channel type makes notify.Send fail → 500.
		s := testServer()
		rec := httptest.NewRecorder()
		req := grpAReq("POST", "/x", map[string]any{"type": "nonexistent-channel"})
		s.handleNotifyTest(rec, withAdminContext(req))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestGrpA_NotifyPrefs(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleNotifyPrefsGet(rec, withAdminContext(grpAReq("GET", "/x", nil)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("put_reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleNotifyPrefsPut(rec, withResellerContext(grpAReq("PUT", "/x", map[string]any{})))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("put_invalid_json", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", "/x", strings.NewReader("{bad"))
		s.handleNotifyPrefsPut(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("put_success", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleNotifyPrefsPut(rec, withAdminContext(grpAReq("PUT", "/x", map[string]any{"webhooks": []any{}})))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestGrpA_Branding(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleBrandingGet(rec, withAdminContext(grpAReq("GET", "/x", nil)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("put_reseller_forbidden", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleBrandingPut(rec, withResellerContext(grpAReq("PUT", "/x", map[string]any{})))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("put_invalid_json", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", "/x", strings.NewReader("{bad"))
		s.handleBrandingPut(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("put_success", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleBrandingPut(rec, withAdminContext(grpAReq("PUT", "/x", map[string]any{"product_name": "MyPanel"})))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestGrpA_RecoveryCodes(t *testing.T) {
	t.Run("generate", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleGenRecoveryCodes(rec, withAdminContext(grpAReq("POST", "/x", nil)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		m := grpADecode(rec)
		codes, _ := m["codes"].([]any)
		if len(codes) != 8 {
			t.Fatalf("codes len = %d, want 8", len(codes))
		}
	})

	t.Run("use_invalid_json", func(t *testing.T) {
		s := testServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", strings.NewReader("{bad"))
		s.handleUseRecoveryCode(rec, withAdminContext(req))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("use_invalid_code", func(t *testing.T) {
		s := testServer()
		s.config.Global.Admin.RecoveryCodes = []string{"valid123"}
		rec := httptest.NewRecorder()
		s.handleUseRecoveryCode(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{"code": "wrong"})))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("use_valid_code", func(t *testing.T) {
		s := testServer()
		s.config.Global.Admin.RecoveryCodes = []string{"valid123", "other"}
		rec := httptest.NewRecorder()
		s.handleUseRecoveryCode(rec, withAdminContext(grpAReq("POST", "/x", map[string]string{"code": "valid123"})))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if len(s.config.Global.Admin.RecoveryCodes) != 1 {
			t.Fatalf("recovery codes left = %d, want 1", len(s.config.Global.Admin.RecoveryCodes))
		}
	})
}
