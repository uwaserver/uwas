package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHtaccessIPAccessEnforced: IP-based .htaccess access control used to be
// ignored — "Require ip"/"Allow from <cidr>" allowlists (top level and in
// <Files wp-login.php>) let every client through, and "Deny from all" +
// "Allow from <ip>" locked the allowlisted client out. Unevaluable forms
// such as "Require host" must fail closed.
func TestHtaccessIPAccessEnforced(t *testing.T) {
	const outsider = "203.0.113.50:4000"
	const allowed = "198.51.100.7:4000"
	login := "<Files wp-login.php>\nRequire ip 198.51.100.7\n</Files>\n"
	cases := []struct {
		name, ip, path string
		files          map[string]string
		want           int
	}{
		{"Require ip, allowed", allowed, "/a.txt", map[string]string{".htaccess": "Require ip 198.51.100.7\n", "a.txt": "x"}, http.StatusOK},
		{"Require ip, outsider", outsider, "/a.txt", map[string]string{".htaccess": "Require ip 198.51.100.7\n", "a.txt": "x"}, http.StatusForbidden},
		{"wp-admin Require ip, outsider", outsider, "/wp-admin/x.php", map[string]string{"wp-admin/.htaccess": "Require ip 198.51.100.0/24\n", "wp-admin/x.php": "x"}, http.StatusForbidden},
		{"Order allow,deny Allow cidr, outsider", outsider, "/a.txt", map[string]string{".htaccess": "Order allow,deny\nAllow from 198.51.100.0/24\n", "a.txt": "x"}, http.StatusForbidden},
		{"Deny all + Allow ip, allowed", allowed, "/a.txt", map[string]string{".htaccess": "Order deny,allow\nDeny from all\nAllow from 198.51.100.7\n", "a.txt": "x"}, http.StatusOK},
		{"Allow all + Deny ip, that ip", outsider, "/a.txt", map[string]string{".htaccess": "Order allow,deny\nAllow from all\nDeny from 203.0.113.50\n", "a.txt": "x"}, http.StatusForbidden},
		{"RequireAll not ip", outsider, "/a.txt", map[string]string{".htaccess": "<RequireAll>\nRequire all granted\nRequire not ip 203.0.113.0/24\n</RequireAll>\n", "a.txt": "x"}, http.StatusForbidden},
		{"Require host fails closed", allowed, "/a.txt", map[string]string{".htaccess": "Require host example.com\n", "a.txt": "x"}, http.StatusForbidden},
		{"Require valid-user stays with the auth gate", outsider, "/a.txt", map[string]string{".htaccess": "Require valid-user\n", "a.txt": "x"}, http.StatusOK},
		{"<Files> Require ip, outsider", outsider, "/wp-login.php", map[string]string{".htaccess": login, "wp-login.php": "x"}, http.StatusForbidden},
		{"<Files> Require ip, other file", outsider, "/a.txt", map[string]string{".htaccess": login, "a.txt": "x"}, http.StatusOK},
		{"<Files> Deny one ip, other client", allowed, "/a.txt", map[string]string{".htaccess": "<Files a.txt>\nOrder allow,deny\nAllow from all\nDeny from 203.0.113.50\n</Files>\n", "a.txt": "x"}, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ran := htaccessAccessSite(t, c.files)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", c.path, nil)
			req.Host = "htaccess-access.test"
			req.RemoteAddr = c.ip
			s.handleRequest(rec, req)
			if rec.Code != c.want {
				t.Fatalf("GET %s from %s = %d, want %d", c.path, c.ip, rec.Code, c.want)
			}
			if c.want == http.StatusForbidden {
				select {
				case script := <-ran:
					t.Fatalf("denied request still executed %s", script)
				default:
				}
			}
		})
	}
}
