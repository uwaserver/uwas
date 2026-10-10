package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHtaccessRedirectStaysOnHost: mod_alias Redirect/RedirectMatch build the
// Location from the decoded request path (suffix append, $N backrefs). With a
// scheme-less target that used to yield a protocol-relative "//evil.com" (or
// "/\evil.com"), an off-site open redirect, e.g. "Redirect 301 /old /" on
// "/old/evil.com".
func TestHtaccessRedirectStaysOnHost(t *testing.T) {
	cases := []struct {
		name, ht, path, want string
	}{
		{"prefix suffix //", "Redirect 301 /old-section /\n", "/old-section/evil.com", "/evil.com"},
		{"prefix suffix backslash", "Redirect 301 /old-section /\n", "/old-section/%5Cevil.com", "/evil.com"},
		{"regex backref //", "RedirectMatch 301 ^/blog(/.*)$ $1\n", "/blog//evil.com", "/evil.com"},
		{"regex backref %2F", "RedirectMatch 301 ^/blog(/.*)$ $1\n", "/blog/%2Fevil.com", "/evil.com"},
		{"normal suffix append", "Redirect 301 /old /new\n", "/old/page", "/new/page"},
		{"absolute target unchanged", "Redirect 301 /go https://example.org\n", "/go//x", "https://example.org//x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := htaccessAccessSite(t, map[string]string{".htaccess": c.ht, "index.php": "<?php"})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", c.path, nil)
			req.Host = "htaccess-access.test"
			s.handleRequest(rec, req)
			if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != c.want {
				t.Fatalf("got %d Location %q, want 301 %q", rec.Code, rec.Header().Get("Location"), c.want)
			}
		})
	}
}
