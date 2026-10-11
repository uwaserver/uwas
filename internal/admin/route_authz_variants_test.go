package admin

// Route-level authorization through the real auth middleware + mux: a route a
// role is refused (401/403) must stay refused under path variants (trailing
// slash, //, ./, ../, upper case, percent-encoding), and the command-running
// endpoints must refuse non-admin roles. Follows F2290/F2500, where a guard
// that compared the raw request differently from the router was bypassed; no
// defect was found in the admin mux, so this pins it.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

func TestAdminRouteAuthzVariantsDenied(t *testing.T) {
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	_ = os.MkdirAll(webRoot, 0o755)
	cfg := &config.Config{Global: config.GlobalConfig{WebRoot: webRoot, LogLevel: "info", LogFormat: "text"},
		Domains: []config.Domain{{Host: "victim.example", Type: "static", Root: filepath.Join(webRoot, "victim.example", "public_html"), SSL: config.SSLConfig{Mode: "off"}}}}
	cfg.Global.Admin.APIKey = "adminkey-0123456789"
	cfg.Global.Users.Enabled = true
	s := testServerFromConfig(t, cfg)
	s.configPath = filepath.Join(dir, "uwas.yaml")
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw-correct-horse"), bcrypt.MinCost)
	users := []*auth.User{
		{ID: "id-lo", Username: "lowuser", Password: string(hash), Role: auth.RoleUser, Enabled: true, CreatedAt: time.Now()},
		{ID: "id-re", Username: "resel", Password: string(hash), Role: auth.RoleReseller, Enabled: true, Domains: []string{"r.example"}, CreatedAt: time.Now()},
	}
	adir := filepath.Join(dir, "auth")
	_ = os.MkdirAll(adir, 0o755)
	data, _ := json.Marshal(users)
	_ = os.WriteFile(filepath.Join(adir, "users.json"), data, 0o600)
	mgr, err := auth.NewManager(adir, "adminkey-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	s.SetAuthManager(mgr)
	realMux := s.mux.(*testMux).mux
	h := s.authMiddleware(requireJSONMiddleware(realMux))

	tok := map[string]string{}
	for _, u := range []string{"lowuser", "resel"} {
		sess, err := mgr.Authenticate(u, "pw-correct-horse")
		if err != nil {
			t.Fatal(err)
		}
		tok[u] = sess.Token
	}

	src, _ := os.ReadFile("routes.go")
	re := regexp.MustCompile(`Handle(?:Func)?\("([A-Z]+) (/\S*)"`)
	type rt struct{ m, p string }
	var routes []rt
	for _, mm := range re.FindAllStringSubmatch(string(src), -1) {
		p := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(mm[2], "x1")
		routes = append(routes, rt{mm[1], p})
	}

	ctr := 0
	do := func(method, path, user string, hdr map[string]string) (code int, reached bool) {
		defer func() {
			if r := recover(); r != nil {
				code, reached = -1, true
			}
		}()
		req := httptest.NewRequest(method, "http://admin.local"+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		ctr++
		req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:5555", (ctr>>16)&255, (ctr>>8)&255, ctr&255)
		if user != "" {
			req.Header.Set("X-Session-Token", tok[user])
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, false
	}
	denied := func(c int) bool { return c == 401 || c == 403 }
	safeVariant := func(c int) bool {
		return denied(c) || c == 404 || c == 405 || c == 301 || c == 307 || c == 308 || c == 400 && false
	}

	variants := func(p string) map[string]string {
		rest := strings.TrimPrefix(p, "/api/v1/")
		v := map[string]string{
			"trailing-slash": p + "/",
			"double-slash":   "/" + p,
			"dot-seg":        "/api/v1/./" + rest,
			"dotdot":         "/api/v1/zz/../" + rest,
			"upper":          strings.ToUpper(p),
			"enc-first":      "/api/v1/%" + fmt.Sprintf("%02x", rest[0]) + rest[1:],
			"enc-slash":      "/api%2Fv1/" + rest,
			"enc-v1":         "/api/%76" + "1/" + rest,
			"query-suffix":   p + "?x=/health",
			"fragmentish":    p + "%23/health",
		}
		return v
	}

	var bad []string
	for _, user := range []string{"", "lowuser", "resel"} {
		for _, r := range routes {
			if !strings.HasPrefix(r.p, "/api/v1/") {
				continue
			}
			base, _ := do(r.m, r.p, user, nil)
			if !denied(base) {
				continue
			}
			for name, vp := range variants(r.p) {
				c, reached := do(r.m, vp, user, nil)
				if !safeVariant(c) || reached {
					bad = append(bad, fmt.Sprintf("user=%q %s %s variant=%s -> %d reached=%v (baseline %d)", user, r.m, r.p, name, c, reached, base))
				}
			}
		}
	}
	nden := 0
	for _, user := range []string{"", "lowuser", "resel"} {
		for _, r := range routes {
			if c, _ := do(r.m, r.p, user, nil); denied(c) {
				nden++
			}
		}
	}
	if nden < 300 {
		t.Fatalf("only %d denied baselines; the matrix is not exercising the guards", nden)
	}

	// Command-running endpoints must refuse every non-admin role outright.
	for _, user := range []string{"", "lowuser", "resel"} {
		for _, e := range []struct{ m, p string }{
			{"POST", "/api/v1/cron/execute"}, {"POST", "/api/v1/cron"}, {"DELETE", "/api/v1/cron"},
			{"GET", "/api/v1/terminal"}, {"POST", "/api/v1/apps/x1/start"}, {"POST", "/api/v1/apps/x1/deploy"},
			{"POST", "/api/v1/php/install"}, {"POST", "/api/v1/database/create"}, {"POST", "/api/v1/wordpress/install"},
			{"POST", "/api/v1/backups"}, {"POST", "/api/v1/reload"},
		} {
			if c, _ := do(e.m, e.p, user, nil); !denied(c) {
				t.Errorf("user=%q %s %s -> %d, want 401/403", user, e.m, e.p, c)
			}
		}
	}

	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
}
