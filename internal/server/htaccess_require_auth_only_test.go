package server

import (
	"net/http"
	"testing"
)

// F2801: an authentication-based Require (valid-user, user, group) that is the
// only access rule is a requirement UWAS cannot verify. It used to be ignored,
// serving the protected directory to anyone. It now fails closed; an IP rule
// next to it (Apache's implicit RequireAny) still lets matching clients in.
func TestHtaccessAuthOnlyRequireFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		ht   string
		want int
	}{
		{"valid-user", "AuthType Basic\nAuthName x\nRequire valid-user\n", http.StatusForbidden},
		{"user", "Require user bob\n", http.StatusForbidden},
		{"group", "Require group admins\n", http.StatusForbidden},
		{"not user", "Require not user mallory\n", http.StatusForbidden},
		{"inside RequireAll", "<RequireAll>\nRequire valid-user\n</RequireAll>\n", http.StatusForbidden},
		{"inside active IfModule", "<IfModule mod_authz_core.c>\nRequire valid-user\n</IfModule>\n", http.StatusForbidden},
		{"inside skipped IfModule (control)", "<IfModule mod_not_loaded_anywhere.c>\nRequire valid-user\n</IfModule>\n", http.StatusOK},
		{"ip rule beside valid-user lets the matching client in", "Require ip 192.0.2.0/24\nRequire valid-user\n", http.StatusOK},
		{"ip rule beside valid-user still denies others", "Require ip 203.0.113.0/24\nRequire valid-user\n", http.StatusForbidden},
		{"all granted (control)", "Require all granted\n", http.StatusOK},
		{"no rules (control)", "Options -Indexes\n", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := phpAuthTestServer(t, authTestRoot(t, tc.ht, map[string]string{"secret.html": "SECRET"}))
			if rec := authGet(t, h, "/secret.html"); rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
