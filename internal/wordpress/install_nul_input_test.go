package wordpress

// F51300: POST /wordpress/install takes db_pass and db_host from the request
// body (a domain owner may call it for their own domain) and Install runs on a
// goroutine with no recover. createMySQLDB put them through escSQL, which
// panics on a NUL byte, so one request with "db_pass":"a\u0000b" ended the
// whole server process for every tenant. A NUL byte can never be part of a
// MySQL literal; it must be refused as bad input, not crash the process.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func installFixture(t *testing.T) {
	t.Helper()
	origCmd, origLook, origExists := execCommandFn, execLookPathFn, databaseExistsFn
	t.Cleanup(func() { execCommandFn, execLookPathFn, databaseExistsFn = origCmd, origLook, origExists })
	execCommandFn = fakeCmd("")
	execLookPathFn = func(string) (string, error) { return "/usr/bin/mysql", nil } // mysql client present
	databaseExistsFn = func(string) bool { return false }
}

// installCaught runs Install and reports a panic instead of letting it end the test binary.
func installCaught(req InstallRequest) (res InstallResult, panicked any) {
	defer func() { panicked = recover() }()
	return Install(req), nil
}

func TestInstallRejectsNULInDBCredentialsWithoutPanicking(t *testing.T) {
	cases := []struct {
		name string
		req  InstallRequest
	}{
		{"db_pass", InstallRequest{DBPass: "a\x00b"}},
		{"db_host", InstallRequest{DBPass: "ok", DBHost: "local\x00host"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			installFixture(t)
			c.req.Domain = "tenant.test"
			c.req.WebRoot = t.TempDir()
			res, p := installCaught(c.req)
			t.Logf("EXPECTED: error result, no panic\nACTUAL: panic=%v status=%q error=%q", p, res.Status, res.Error)
			if p != nil {
				t.Fatalf("PROBLEM CONFIRMED: Install panicked on a NUL byte in %s: %v", c.name, p)
			}
			if res.Status != "error" || !strings.Contains(res.Error, "NUL") {
				t.Fatalf("status=%q error=%q, want an error naming the NUL byte", res.Status, res.Error)
			}
		})
	}
}

func TestInstallStillAcceptsOrdinaryCredentials(t *testing.T) {
	installFixture(t)
	// Control: a normal password with quotes and backslashes reaches createMySQLDB
	// and the install proceeds (it fails later at the download, which is stubbed out).
	origGet := httpGetFn
	defer func() { httpGetFn = origGet }()
	httpGetFn = func(string) (*http.Response, error) { return nil, fmt.Errorf("download stubbed") }
	res, p := installCaught(InstallRequest{Domain: "tenant.test", WebRoot: t.TempDir(), DBPass: `it's a "pass" \ ok`, DBHost: "localhost"})
	if p != nil {
		t.Fatalf("panic on an ordinary password: %v", p)
	}
	if strings.Contains(res.Error, "NUL") {
		t.Fatalf("ordinary credentials were refused as NUL: %q", res.Error)
	}
}
