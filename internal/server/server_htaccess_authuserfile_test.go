package server

// Durable end-to-end coverage for the .htaccess AuthUserFile fail-closed gate.
//
// AuthUserFile was parsed into RuleSet (pkg/htaccess/converter.go:232, behind a
// traversal guard) and merged (:307) but had zero consumers, so a directory an
// operator protected with "AuthType Basic" / "AuthUserFile .htpasswd" /
// "Require valid-user" served its contents to anyone with the URL. The gate now
// 403s at handleFileRequest (internal/server/server_dispatch.go, just before the
// PHP branch).
//
// The PHP cases below are what prove the gate is positioned correctly. The gate
// sits BEFORE the `domain.Type == "php"` branch, so a protected .php request is
// denied by the gate and never reaches s.php.Serve — meaning no FPM backend has
// to be running for the assertion to be meaningful. That is also what makes the
// discriminator sharp: without the gate the same request falls through to the PHP
// branch and fails with a DIFFERENT status (502, FPM unreachable), so asserting
// "403, and specifically not 502" distinguishes the gate from an incidental
// upstream failure.
//
// Requests must set a User-Agent: botguard 403s empty UAs before dispatch.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

const authUserFileHT = `AuthType Basic
AuthName "Private Area"
AuthUserFile .htpasswd
Require valid-user
`

// phpAuthTestServer serves root as a PHP domain through the real middleware
// chain + dispatch path. No FPM instance is configured or required.
func phpAuthTestServer(t *testing.T, root string) http.Handler {
	t.Helper()
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:     "auth.test",
			Type:     "php",
			Root:     root,
			SSL:      config.SSLConfig{Mode: "off"},
			Htaccess: config.HtaccessConfig{Mode: "import"},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	return s.buildMiddlewareChain()
}

func authTestRoot(t *testing.T, htaccessBody string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if htaccessBody != "" {
		if err := os.WriteFile(filepath.Join(root, ".htaccess"), []byte(htaccessBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func authGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "auth.test"
	req.Header.Set("User-Agent", "Mozilla/5.0 (test)")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestHtaccessAuthUserFileDeniesPHPRequest is the PHP-branch case.
//
// A .php request under an AuthUserFile directory must be denied by the gate.
// Asserting it is exactly 403 AND not 502 is what proves the gate fired before
// the PHP handler: with no FPM backend the PHP branch can only produce 502, so a
// 502 here would mean the request slipped past the gate and reached FastCGI.
func TestHtaccessAuthUserFileDeniesPHPRequest(t *testing.T) {
	root := authTestRoot(t, authUserFileHT, map[string]string{
		"secret.php": "<?php echo 'TOP SECRET admin dashboard'; ?>",
		"other.php":  "<?php echo 'another private script'; ?>",
	})
	h := phpAuthTestServer(t, root)

	for _, p := range []string{"/secret.php", "/other.php"} {
		rec := authGet(t, h, p)

		if rec.Code != http.StatusForbidden {
			t.Errorf("PHP: GET %s got status=%d, want 403. "+
				"If this is 502 the request reached the PHP/FPM branch, meaning the "+
				"AuthUserFile gate did not fire first; if 200 the protected script "+
				"was served without authentication.", p, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "TOP SECRET") || strings.Contains(body, "another private script") {
			t.Errorf("PHP: GET %s leaked protected PHP source/output: %q", p, body)
		}
	}
}

// TestHtaccessAuthUserFileDeniesStaticRequest is the sibling static case, so the
// gate's coverage of both domain types is pinned in one file.
func TestHtaccessAuthUserFileDeniesStaticRequest(t *testing.T) {
	root := authTestRoot(t, authUserFileHT, map[string]string{
		"private.html": "TOP SECRET admin dashboard",
	})
	h := phpAuthTestServer(t, root) // PHP domain, static asset

	rec := authGet(t, h, "/private.html")
	if rec.Code != http.StatusForbidden {
		t.Errorf("Static asset under a PHP domain's AuthUserFile directory: got status=%d, want 403",
			rec.Code)
	}
	if strings.Contains(rec.Body.String(), "TOP SECRET") {
		t.Errorf("leaked protected file contents: %q", rec.Body.String())
	}
}

// TestHtaccessAuthUserFilePHPControlNoAuthDirective is the control that makes the
// PHP assertions meaningful: the same .php request on the same PHP domain, with
// no AuthUserFile configured, must NOT be 403 by the gate. It falls through to
// the PHP branch and fails with 502 (no FPM backend), which proves the 403 in
// the tests above comes from the gate and not from an incidental failure.
func TestHtaccessAuthUserFilePHPControlNoAuthDirective(t *testing.T) {
	ht := "DirectoryIndex index.php\n"
	root := authTestRoot(t, ht, map[string]string{
		"open.php": "<?php echo 'public'; ?>",
	})
	h := phpAuthTestServer(t, root)

	rec := authGet(t, h, "/open.php")

	// Assert the EXACT status, not merely "not 403". With no AuthUserFile the
	// gate must be inert, so the request reaches the PHP branch, which has no
	// FastCGI backend here and therefore answers 502. A loose "!== 403" check
	// would also accept a 401 — i.e. it would pass even if the gate had
	// started challenging instead of staying out of the way, which is exactly
	// the over-broad behaviour this control exists to catch. Pinning 502
	// proves the request genuinely traversed the PHP handler.
	if rec.Code != http.StatusBadGateway {
		t.Errorf("control failed: with no AuthUserFile configured the gate must be inert "+
			"and the request must reach the PHP branch, which returns 502 with no FastCGI "+
			"backend. Got %d. A 403 means the gate is over-broad (keying off something "+
			"other than the validated AuthUserFile path); a 401 means it started "+
			"challenging instead of staying inert. Body: %q", rec.Code, rec.Body.String())
	}
}

// TestHtaccessAuthUserFilePHPRejectedPathStillGates pins that an absolute or
// traversal AuthUserFile is still an authentication requirement. The converter
// never keeps (or reads) such a path, but dropping the whole requirement with
// it served a password-protected directory to anyone (F2800). A tenant's
// .htaccess only governs its own docroot, so gating it is not a DoS vector.
func TestHtaccessAuthUserFilePHPRejectedPathStillGates(t *testing.T) {
	cases := []struct {
		name string
		ht   string
	}{
		{"traversal", "AuthType Basic\nAuthUserFile ../../../etc/passwd\nRequire valid-user\n"},
		{"absolute", "AuthType Basic\nAuthUserFile /etc/shadow\nRequire valid-user\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := authTestRoot(t, tc.ht, map[string]string{"open.php": "<?php echo 'public'; ?>"})
			h := phpAuthTestServer(t, root)

			rec := authGet(t, h, "/open.php")
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s AuthUserFile must fail closed with 403 like the relative form, got %d", tc.name, rec.Code)
			}
		})
	}
}

// TestHtaccessAuthUserFilePHPControlNoHtaccess covers the base case: a PHP site
// with no .htaccess at all must never be denied by this gate.
func TestHtaccessAuthUserFilePHPControlNoHtaccess(t *testing.T) {
	root := authTestRoot(t, "", map[string]string{"open.php": "<?php echo 'public'; ?>"})
	h := phpAuthTestServer(t, root)

	rec := authGet(t, h, "/open.php")
	if rec.Code == http.StatusForbidden {
		t.Errorf("control failed: a PHP site with no .htaccess got 403; the gate must be inert. body=%q",
			rec.Body.String())
	}
}
