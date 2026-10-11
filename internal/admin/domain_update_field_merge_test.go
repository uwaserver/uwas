package admin

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F2440: MergeDomain copied only part of config.Domain, so PUT
// /api/v1/domains/{host} answered 200 and echoed the merged domain while
// rewrites, headers, error_pages, try_files, spa_mode, index_files,
// directory_listing, cors, bandwidth, maintenance, image_optimization,
// access_log, internal_aliases and webhook_secret were silently not stored.
func TestUpdateAppliesEveryDomainField(t *testing.T) {
	type tc struct {
		key      string
		set      string
		seed     func(d *config.Domain)
		clear    string
		field    func(d config.Domain) any
		wantZero any
	}
	cases := []tc{
		{"rewrites", `{"rewrites":[{"match":"^/a","to":"/b"}]}`, func(d *config.Domain) { d.Rewrites = []config.RewriteRule{{Match: "^/x", To: "/y"}} },
			`{"rewrites":[]}`, func(d config.Domain) any { return len(d.Rewrites) }, 0},
		{"headers", `{"headers":{"response_add":{"X-A":"1"}}}`, func(d *config.Domain) { d.Headers.ResponseAdd = map[string]string{"X-Z": "1"} },
			`{"headers":{}}`, func(d config.Domain) any { return len(d.Headers.ResponseAdd) }, 0},
		{"error_pages", `{"error_pages":{"404":"/e.html"}}`, func(d *config.Domain) { d.ErrorPages = map[int]string{500: "/s.html"} },
			`{"error_pages":{}}`, func(d config.Domain) any { return len(d.ErrorPages) }, 0},
		{"try_files", `{"try_files":["$uri","/i.html"]}`, func(d *config.Domain) { d.TryFiles = []string{"$uri"} },
			`{"try_files":[]}`, func(d config.Domain) any { return len(d.TryFiles) }, 0},
		{"spa_mode", `{"spa_mode":true}`, func(d *config.Domain) { d.SPAMode = true },
			`{"spa_mode":false}`, func(d config.Domain) any { return d.SPAMode }, false},
		{"index_files", `{"index_files":["home.html"]}`, func(d *config.Domain) { d.IndexFiles = []string{"i.html"} },
			`{"index_files":[]}`, func(d config.Domain) any { return len(d.IndexFiles) }, 0},
		{"directory_listing", `{"directory_listing":true}`, func(d *config.Domain) { d.DirectoryListing = true },
			`{"directory_listing":false}`, func(d config.Domain) any { return d.DirectoryListing }, false},
		{"cors", `{"cors":{"enabled":true,"allowed_origins":["https://x"]}}`, func(d *config.Domain) { d.CORS.Enabled = true },
			`{"cors":{"enabled":false}}`, func(d config.Domain) any { return d.CORS.Enabled }, false},
		{"bandwidth", `{"bandwidth":{"enabled":true,"monthly_limit":5}}`, func(d *config.Domain) { d.Bandwidth.Enabled = true },
			`{"bandwidth":{"enabled":false}}`, func(d config.Domain) any { return d.Bandwidth.Enabled }, false},
		{"maintenance", `{"maintenance":{"enabled":true}}`, func(d *config.Domain) { d.Maintenance.Enabled = true },
			`{"maintenance":{"enabled":false}}`, func(d config.Domain) any { return d.Maintenance.Enabled }, false},
		{"image_optimization", `{"image_optimization":{"enabled":true}}`, func(d *config.Domain) { d.ImageOptimization.Enabled = true },
			`{"image_optimization":{"enabled":false}}`, func(d config.Domain) any { return d.ImageOptimization.Enabled }, false},
		{"access_log", `{"access_log":{"path":"/var/log/x.log"}}`, func(d *config.Domain) { d.AccessLog.Path = "/var/log/old.log" },
			`{"access_log":{}}`, func(d config.Domain) any { return d.AccessLog.Path }, ""},
		{"internal_aliases", `{"internal_aliases":["/protected"]}`, func(d *config.Domain) { d.InternalAliases = []string{"/p"} },
			`{"internal_aliases":[]}`, func(d config.Domain) any { return len(d.InternalAliases) }, 0},
		{"webhook_secret", `{"webhook_secret":"s3"}`, func(d *config.Domain) { d.WebhookSecret = "old" },
			`{"webhook_secret":""}`, func(d config.Domain) any { return d.WebhookSecret }, ""},
	}

	stored := func(e *hostConflictEnv) config.Domain {
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		return e.s.config.Domains[0]
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			// Set: the sent value is stored.
			e := newHostConflictEnv(t, []config.Domain{{Host: "a.com", Type: "static"}})
			if code := e.do(http.MethodPut, "/api/v1/domains/a.com", c.set, auth.RoleAdmin); code != http.StatusOK {
				t.Fatalf("set status = %d", code)
			}
			if got := c.field(stored(e)); reflect.DeepEqual(got, c.wantZero) {
				t.Errorf("set: %s not stored (got zero value %v)", c.key, got)
			}

			// Clear: an explicit empty value replaces a stored non-zero one.
			e = newHostConflictEnv(t, []config.Domain{{Host: "a.com", Type: "static"}})
			e.s.configMu.Lock()
			c.seed(&e.s.config.Domains[0])
			e.s.configMu.Unlock()
			if reflect.DeepEqual(c.field(stored(e)), c.wantZero) {
				t.Fatalf("seed for %s left the zero value", c.key)
			}
			if code := e.do(http.MethodPut, "/api/v1/domains/a.com", c.clear, auth.RoleAdmin); code != http.StatusOK {
				t.Fatalf("clear status = %d", code)
			}
			if got := c.field(stored(e)); !reflect.DeepEqual(got, c.wantZero) {
				t.Errorf("clear: %s = %v, want %v", c.key, got, c.wantZero)
			}

			// Omit: an update that does not mention the key keeps it.
			e = newHostConflictEnv(t, []config.Domain{{Host: "a.com", Type: "static"}})
			e.s.configMu.Lock()
			c.seed(&e.s.config.Domains[0])
			e.s.configMu.Unlock()
			want := c.field(stored(e))
			if code := e.do(http.MethodPut, "/api/v1/domains/a.com", `{"cache":{"enabled":true,"ttl":60}}`, auth.RoleAdmin); code != http.StatusOK {
				t.Fatalf("omit status = %d", code)
			}
			if got := c.field(stored(e)); !reflect.DeepEqual(got, want) {
				t.Errorf("omit: %s changed from %v to %v", c.key, want, got)
			}
		})
	}
}

// A reseller still cannot set the fields Update refuses from non-admins, and
// the newly applied ones do not reopen them.
func TestUpdateFieldMergeKeepsResellerGuards(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{{Host: "a.com", Type: "static"}})
	for _, body := range []string{
		`{"access_log":{"path":"/var/log/evil.log"}}`,
		`{"Internal_Aliases":["/etc"]}`,
		`{"webhook_secret":"mine"}`,
		`{"error_pages":{"404":"../../etc/passwd"}}`,
	} {
		if code := e.do(http.MethodPut, "/api/v1/domains/a.com", body, auth.RoleReseller, "a.com"); code != http.StatusForbidden {
			t.Errorf("reseller %s: status = %d, want 403", body, code)
		}
	}
	d := func() config.Domain {
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		return e.s.config.Domains[0]
	}()
	if d.AccessLog.Path != "" || len(d.InternalAliases) != 0 || d.WebhookSecret != "" || len(d.ErrorPages) != 0 {
		t.Errorf("reseller changed guarded fields: %+v", d)
	}
	// A harmless field a reseller may set is now actually stored.
	if code := e.do(http.MethodPut, "/api/v1/domains/a.com", `{"spa_mode":true}`, auth.RoleReseller, "a.com"); code != http.StatusOK {
		t.Fatalf("reseller spa_mode status = %d", code)
	}
	e.s.configMu.RLock()
	spa := e.s.config.Domains[0].SPAMode
	e.s.configMu.RUnlock()
	if !spa {
		t.Error("reseller spa_mode was not stored")
	}
}

// F2441: php.env, php.index_files and app.env were kept whenever the patch
// value was empty, so an explicit empty value answered 200 and cleared
// nothing.
func TestUpdateClearsNestedCollections(t *testing.T) {
	seed := func(t *testing.T) *hostConflictEnv {
		t.Helper()
		e := newHostConflictEnv(t, []config.Domain{{Host: "a.com", Type: "static"}})
		e.s.configMu.Lock()
		d := &e.s.config.Domains[0]
		d.PHP.Env = map[string]string{"OLD": "1"}
		d.PHP.IndexFiles = []string{"index.php"}
		d.App.Env = map[string]string{"OLD": "1"}
		e.s.configMu.Unlock()
		return e
	}
	get := func(e *hostConflictEnv) config.Domain {
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		return e.s.config.Domains[0]
	}
	cases := []struct {
		name, body string
		left       func(d config.Domain) int
	}{
		{"php.env", `{"php":{"env":{}}}`, func(d config.Domain) int { return len(d.PHP.Env) }},
		{"PHP.ENV case variant", `{"PHP":{"ENV":{}}}`, func(d config.Domain) int { return len(d.PHP.Env) }},
		{"php.env null", `{"php":{"env":null}}`, func(d config.Domain) int { return len(d.PHP.Env) }},
		{"php.index_files", `{"php":{"index_files":[]}}`, func(d config.Domain) int { return len(d.PHP.IndexFiles) }},
		{"app.env", `{"app":{"env":{}}}`, func(d config.Domain) int { return len(d.App.Env) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := seed(t)
			if code := e.do(http.MethodPut, "/api/v1/domains/a.com", c.body, auth.RoleAdmin); code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if n := c.left(get(e)); n != 0 {
				t.Errorf("%d entries left, want 0", n)
			}
		})
	}

	t.Run("non-empty replaces, siblings and omitted keys stay", func(t *testing.T) {
		e := seed(t)
		if code := e.do(http.MethodPut, "/api/v1/domains/a.com", `{"php":{"env":{"NEW":"2"}}}`, auth.RoleAdmin); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		d := get(e)
		if len(d.PHP.Env) != 1 || d.PHP.Env["NEW"] != "2" {
			t.Errorf("php.env = %v, want only NEW", d.PHP.Env)
		}
		if len(d.PHP.IndexFiles) != 1 || d.App.Env["OLD"] != "1" {
			t.Errorf("unrelated collections changed: index=%v app.env=%v", d.PHP.IndexFiles, d.App.Env)
		}
	})
	t.Run("unrelated update keeps them", func(t *testing.T) {
		e := seed(t)
		if code := e.do(http.MethodPut, "/api/v1/domains/a.com", `{"php":{"max_upload":10}}`, auth.RoleAdmin); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		d := get(e)
		if d.PHP.Env["OLD"] != "1" || len(d.PHP.IndexFiles) != 1 || d.App.Env["OLD"] != "1" {
			t.Errorf("collections changed by an update that omitted them: %+v / %v", d.PHP, d.App.Env)
		}
	})
}
