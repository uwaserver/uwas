package admin

// F31302: handlePackageInstall treated every action other than the exact string
// "remove" as an install, so {"action":"uninstall"} (or "Remove", "purge")
// answered 200 "uninstalling" and ran `apt install` for the package, and the
// canRemove / removal-method guards were skipped.

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/install"
)

func TestPackageActionIsValidated(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	restore := setSystemExecCommand(func(name string, args ...string) *exec.Cmd {
		mu.Lock()
		ran = append(ran, name+" "+strings.Join(args, " "))
		mu.Unlock()
		return exec.Command("true")
	})
	defer restore()

	post := func(body string) (*Server, *install.Queue, int) {
		s := testServer()
		q := install.New()
		s.taskMgr = q
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/packages/install", strings.NewReader(body))))
		return s, q, rec.Code
	}

	for _, action := range []string{"uninstall", "Remove", "purge", "nonsense"} {
		mu.Lock()
		ran = nil
		mu.Unlock()
		_, q, code := post(`{"id":"nodejs","action":"` + action + `"}`)
		q.Stop()
		mu.Lock()
		got := append([]string(nil), ran...)
		mu.Unlock()
		t.Logf("EXPECTED: 400 and no command\nACTUAL: action=%q status=%d commands=%v", action, code, got)
		if code != http.StatusBadRequest || len(got) != 0 {
			t.Fatalf("PROBLEM CONFIRMED: action %q answered %d and ran %v", action, code, got)
		}
	}

	// Controls: the two documented actions still work.
	for _, c := range []struct{ body string }{
		{`{"id":"webp"}`},
		{`{"id":"webp","action":"install"}`},
		{`{"id":"nodejs","action":"remove"}`},
	} {
		_, q, code := post(c.body)
		q.Stop()
		if code != http.StatusOK {
			t.Fatalf("control %s: status = %d, want 200", c.body, code)
		}
	}
}
