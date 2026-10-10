package apps

// Regression test for F840: a PUT whose patched definition fails validation is rejected
// with 400 BEFORE the running app is touched: same PID, still running, stored
// definition unchanged. Valid patches still restart the app.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

type updInvalidDeps struct{ mgr *apps.Manager }

func (d *updInvalidDeps) RequireAdmin(http.ResponseWriter, *http.Request) bool { return true }
func (d *updInvalidDeps) RequirePin(http.ResponseWriter, *http.Request) bool   { return true }
func (d *updInvalidDeps) LogInfo(string, ...any)                               {}
func (d *updInvalidDeps) LogWarn(string, ...any)                               {}
func (d *updInvalidDeps) LogError(string, ...any)                              {}
func (d *updInvalidDeps) RecordAudit(*http.Request, string, string, bool)      {}
func (d *updInvalidDeps) ParsePagination(*http.Request) (int, int)             { return 50, 0 }
func (d *updInvalidDeps) AppsManager() *apps.Manager                           { return d.mgr }
func (d *updInvalidDeps) Reload() error                                        { return nil }
func (d *updInvalidDeps) ConfigPath() string                                   { return "" }
func (d *updInvalidDeps) ValidateDeployConfig(*apps.App) error                 { return nil }

func updInvalidSetup(t *testing.T, name string, start bool) (*Handler, *apps.Manager) {
	t.Helper()
	dir := t.TempDir()
	mgr := apps.NewManager(apps.NewStore(dir), nil)
	a := &apps.App{Name: name, Runtime: apps.RuntimeCustom, Command: "exec sleep 600", WorkDir: dir, Port: 18840, Disabled: !start}
	if err := mgr.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if start {
		if err := mgr.Start(name); err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	t.Cleanup(func() { _ = mgr.Stop(name) })
	return New(&updInvalidDeps{mgr: mgr}), mgr
}

func updInvalidPut(h *Handler, name, body string) int {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/"+name, strings.NewReader(body))
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	return rec.Code
}

func TestUpdateInvalidPatchKeepsAppRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	ok, bad := 0, 0
	check := func(label string, cond bool, detail string) {
		if cond {
			ok++
			_ = label
		} else {
			bad++
			fmt.Printf("MISMATCH %s: %s\n", label, detail)
		}
	}
	pid := func(mgr *apps.Manager, n string) (int, bool) {
		inst := mgr.Get(n)
		if inst == nil {
			return 0, false
		}
		return inst.PID, inst.Running
	}

	// 1. Reproduction cases, each sent twice: same PID, running, stored def unchanged.
	invalid := []struct{ label, body string }{
		{"port-out-of-range", `{"port":70000}`},
		{"unknown-runtime", `{"runtime":"bogus"}`},
		{"exposed-port-out-of-range", `{"ports":[70000]}`},
		{"docker-host-volume", `{"runtime":"docker","docker":{"image":"x","container_port":80,"volumes":["./etc:/data"]}}`},
		{"docker-missing-container-port", `{"runtime":"docker","docker":{"image":"x"}}`},
	}
	for i, c := range invalid {
		n := fmt.Sprintf("inv%d", i)
		h, mgr := updInvalidSetup(t, n, true)
		pid0, _ := pid(mgr, n)
		for round := 1; round <= 2; round++ {
			code := updInvalidPut(h, n, c.body)
			p, run := pid(mgr, n)
			check(fmt.Sprintf("%s round%d", c.label, round), code == 400 && run && p == pid0,
				fmt.Sprintf("code=%d running=%v pid=%d want pid=%d", code, run, p, pid0))
		}
		def, _ := mgr.Store().Get(n)
		check(c.label+" stored-def-unchanged", def != nil && def.Port == 18840 && def.Runtime == apps.RuntimeCustom,
			fmt.Sprintf("def=%+v", def))
	}

	// 2. Valid patch still restarts the app on the new port.
	{
		h, mgr := updInvalidSetup(t, "valid", true)
		pid0, _ := pid(mgr, "valid")
		code := updInvalidPut(h, "valid", `{"port":18841,"auto_restart":false}`)
		p, run := pid(mgr, "valid")
		def, _ := mgr.Store().Get("valid")
		check("valid-patch-restarts", code == 200 && run && p != pid0 && def != nil && def.Port == 18841,
			fmt.Sprintf("code=%d running=%v pid=%d old=%d def.port=%v", code, run, p, pid0, def))
	}

	// 3. Disabled (stopped) app with invalid patch: 400, stays stopped, def unchanged.
	{
		h, mgr := updInvalidSetup(t, "disabled", false)
		code := updInvalidPut(h, "disabled", `{"port":70000,"disabled":true}`)
		_, run := pid(mgr, "disabled")
		def, _ := mgr.Store().Get("disabled")
		check("disabled-invalid", code == 400 && !run && def != nil && def.Port == 18840,
			fmt.Sprintf("code=%d running=%v def=%+v", code, run, def))
	}

	// 4. 8 concurrent invalid PUTs released together by one channel.
	{
		h, mgr := updInvalidSetup(t, "burst", true)
		pid0, _ := pid(mgr, "burst")
		gate := make(chan struct{})
		var wg sync.WaitGroup
		codes := make([]int, 8)
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-gate
				codes[i] = updInvalidPut(h, "burst", `{"port":70000}`)
			}(i)
		}
		close(gate)
		wg.Wait()
		all400 := true
		for _, c := range codes {
			if c != 400 {
				all400 = false
			}
		}
		p, run := pid(mgr, "burst")
		check("burst-8-invalid", all400 && run && p == pid0, fmt.Sprintf("codes=%v running=%v pid=%d old=%d", codes, run, p, pid0))
	}

	fmt.Printf("OK=%d MISMATCH=%d\n", ok, bad)
	if bad > 0 {
		t.Fatalf("FIX NOT VERIFIED")
	}
	fmt.Println("FIX VERIFIED")
}
