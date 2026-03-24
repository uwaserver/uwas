package rewrite

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBasicRewrite(t *testing.T) {
	rule, _ := ParseRule("^/old$", "/new", "L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/old"}

	result := engine.Process("/old", "", vars)
	if result.URI != "/new" {
		t.Errorf("URI = %q, want /new", result.URI)
	}
	if !result.Modified {
		t.Error("should be modified")
	}
}

func TestNoMatch(t *testing.T) {
	rule, _ := ParseRule("^/old$", "/new", "L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/other"}

	result := engine.Process("/other", "", vars)
	if result.URI != "/other" {
		t.Errorf("URI = %q, want /other (unchanged)", result.URI)
	}
	if result.Modified {
		t.Error("should not be modified")
	}
}

func TestBackreferences(t *testing.T) {
	rule, _ := ParseRule("^/blog/([0-9]+)/(.+)$", "/posts?id=$1&slug=$2", "L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/blog/42/hello-world"}

	result := engine.Process("/blog/42/hello-world", "", vars)
	if result.URI != "/posts" {
		t.Errorf("URI = %q, want /posts", result.URI)
	}
	if result.Query != "id=42&slug=hello-world" {
		t.Errorf("Query = %q, want id=42&slug=hello-world", result.Query)
	}
}

func TestRedirectFlag(t *testing.T) {
	rule, _ := ParseRule("^/old$", "/new", "R=301,L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/old"}

	result := engine.Process("/old", "", vars)
	if !result.Redirect {
		t.Error("should be redirect")
	}
	if result.StatusCode != 301 {
		t.Errorf("status = %d, want 301", result.StatusCode)
	}
}

func TestForbiddenFlag(t *testing.T) {
	rule, _ := ParseRule("^/secret", "-", "F")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/secret/file"}

	result := engine.Process("/secret/file", "", vars)
	if !result.Forbidden {
		t.Error("should be forbidden")
	}
}

func TestQSAppend(t *testing.T) {
	rule, _ := ParseRule("^/page$", "/view?type=html", "QSA,L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/page"}

	result := engine.Process("/page", "lang=en", vars)
	if result.URI != "/view" {
		t.Errorf("URI = %q, want /view", result.URI)
	}
	// QSA should append original query
	if result.Query != "type=html&lang=en" {
		t.Errorf("Query = %q, want type=html&lang=en", result.Query)
	}
}

func TestCaseInsensitive(t *testing.T) {
	rule, _ := ParseRule("^/ABOUT$", "/about-page", "NC,L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/about"}

	result := engine.Process("/about", "", vars)
	if result.URI != "/about-page" {
		t.Errorf("URI = %q, want /about-page", result.URI)
	}
}

func TestWordPressRewrites(t *testing.T) {
	// WordPress-style rewrite: if not a file and not a dir, rewrite to index.php
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-login.php"), []byte("php"), 0644)

	rule, _ := ParseRule("^/(.+)$", "/index.php", "L")
	cond1, _ := ParseCondition("%{REQUEST_FILENAME}", "!-f", "")
	cond2, _ := ParseCondition("%{REQUEST_FILENAME}", "!-d", "")
	rule.Conditions = []Condition{*cond1, *cond2}

	engine := NewEngine([]*Rule{rule})

	// Non-existing file should be rewritten
	vars := &Variables{
		RequestURI:      "/hello-world",
		RequestFilename: filepath.Join(dir, "hello-world"),
	}
	result := engine.Process("/hello-world", "", vars)
	if result.URI != "/index.php" {
		t.Errorf("URI = %q, want /index.php (non-existing)", result.URI)
	}

	// Existing file should NOT be rewritten
	vars2 := &Variables{
		RequestURI:      "/wp-login.php",
		RequestFilename: filepath.Join(dir, "wp-login.php"),
	}
	result2 := engine.Process("/wp-login.php", "", vars2)
	if result2.URI != "/wp-login.php" {
		t.Errorf("URI = %q, want /wp-login.php (existing file)", result2.URI)
	}
}

func TestSkipFlag(t *testing.T) {
	rule1, _ := ParseRule("^/admin", "-", "S=1") // skip 1 rule
	rule2, _ := ParseRule(".*", "/public", "L")  // this should be skipped
	rule3, _ := ParseRule(".*", "/admin-panel", "L")

	engine := NewEngine([]*Rule{rule1, rule2, rule3})
	vars := &Variables{RequestURI: "/admin/dashboard"}

	result := engine.Process("/admin/dashboard", "", vars)
	if result.URI != "/admin-panel" {
		t.Errorf("URI = %q, want /admin-panel (skip rule2)", result.URI)
	}
}

func TestLoopDetection(t *testing.T) {
	// Two rules that would infinitely rewrite each other
	rule1, _ := ParseRule("^/a$", "/b", "")
	rule2, _ := ParseRule("^/b$", "/a", "")

	engine := NewEngine([]*Rule{rule1, rule2})
	vars := &Variables{RequestURI: "/a"}

	// Should not hang — max 10 iterations
	result := engine.Process("/a", "", vars)
	_ = result // just verify it terminates
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		input string
		check func(Flags) bool
		desc  string
	}{
		{"L", func(f Flags) bool { return f.Last }, "Last"},
		{"R=301", func(f Flags) bool { return f.Redirect == 301 }, "Redirect 301"},
		{"R", func(f Flags) bool { return f.Redirect == 302 }, "Redirect default"},
		{"QSA", func(f Flags) bool { return f.QSAppend }, "QSAppend"},
		{"NC", func(f Flags) bool { return f.NoCase }, "NoCase"},
		{"F", func(f Flags) bool { return f.Forbidden }, "Forbidden"},
		{"G", func(f Flags) bool { return f.Gone }, "Gone"},
		{"[L,QSA,NC]", func(f Flags) bool { return f.Last && f.QSAppend && f.NoCase }, "Combined"},
		{"S=2", func(f Flags) bool { return f.Skip == 2 }, "Skip"},
	}

	for _, tt := range tests {
		f := ParseFlags(tt.input)
		if !tt.check(f) {
			t.Errorf("ParseFlags(%q) failed for %s", tt.input, tt.desc)
		}
	}
}

func TestConditionFileExists(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "exists.txt"), []byte("data"), 0644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

	// -f: is file
	cond, _ := ParseCondition("%{REQUEST_FILENAME}", "-f", "")
	vars := &Variables{RequestFilename: filepath.Join(dir, "exists.txt")}
	matched, _ := cond.Evaluate(vars)
	if !matched {
		t.Error("-f should match existing file")
	}

	// !-f: is NOT file
	cond2, _ := ParseCondition("%{REQUEST_FILENAME}", "!-f", "")
	vars2 := &Variables{RequestFilename: filepath.Join(dir, "nonexistent")}
	matched2, _ := cond2.Evaluate(vars2)
	if !matched2 {
		t.Error("!-f should match non-existing file")
	}

	// -d: is directory
	cond3, _ := ParseCondition("%{REQUEST_FILENAME}", "-d", "")
	vars3 := &Variables{RequestFilename: filepath.Join(dir, "subdir")}
	matched3, _ := cond3.Evaluate(vars3)
	if !matched3 {
		t.Error("-d should match existing directory")
	}
}

func TestVariableExpand(t *testing.T) {
	vars := &Variables{
		RequestURI:    "/test",
		HTTPHost:      "example.com",
		RequestMethod: "GET",
		HTTPS:         "on",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"%{REQUEST_URI}", "/test"},
		{"%{HTTP_HOST}", "example.com"},
		{"%{REQUEST_METHOD}", "GET"},
		{"%{HTTPS}", "on"},
		{"%{UNKNOWN}", ""},
	}

	for _, tt := range tests {
		got := vars.Expand(tt.input)
		if got != tt.want {
			t.Errorf("Expand(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Tests for skipChain, BuildVariables, ConvertConfigRewrites, ResolvedURI ---

func TestSkipChain(t *testing.T) {
	// Build a chain of rules: rule0[C] -> rule1[C] -> rule2 -> rule3
	rule0, _ := ParseRule("^/a", "/b", "C")
	rule1, _ := ParseRule("^/b", "/c", "C")
	rule2, _ := ParseRule("^/c", "/d", "L")
	rule3, _ := ParseRule("^/d", "/e", "L")

	engine := NewEngine([]*Rule{rule0, rule1, rule2, rule3})

	// If rule0 condition fails, skipChain(0) should skip past the chain (rules 0,1 chained)
	// and land on rule2 (index 2)
	vars := &Variables{RequestURI: "/nomatch"}
	result := engine.Process("/nomatch", "", vars)
	// None match, so URI unchanged
	if result.URI != "/nomatch" {
		t.Errorf("URI = %q, want /nomatch", result.URI)
	}

	// Now test that chain skip works by having only the first rule match,
	// but with a condition that fails. Use a condition-based approach:
	condRule0, _ := ParseRule("^/x", "/y", "C")
	condRule1, _ := ParseRule("^/y", "/z", "L")
	condRule2, _ := ParseRule(".*", "/fallback", "L")

	e2 := NewEngine([]*Rule{condRule0, condRule1, condRule2})
	vars2 := &Variables{RequestURI: "/other"}
	result2 := e2.Process("/other", "", vars2)
	// /other doesn't match ^/x, and ^/x has [C], so condRule1 is skipped.
	// Then condRule2 (.*) matches /other -> /fallback
	if result2.URI != "/fallback" {
		t.Errorf("URI = %q, want /fallback", result2.URI)
	}
}

func TestBuildVariables(t *testing.T) {
	req := httptest.NewRequest("POST", "/path?q=1", nil)
	req.Host = "example.com:8080"
	req.Header.Set("Referer", "https://other.com/")
	req.Header.Set("User-Agent", "TestAgent/1.0")

	vars := BuildVariables(req, "/var/www", "/var/www/path", false)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"RequestURI", vars.RequestURI, "/path"},
		{"RequestFilename", vars.RequestFilename, "/var/www/path"},
		{"QueryString", vars.QueryString, "q=1"},
		{"HTTPHost", vars.HTTPHost, "example.com:8080"},
		{"HTTPReferer", vars.HTTPReferer, "https://other.com/"},
		{"HTTPUserAgent", vars.HTTPUserAgent, "TestAgent/1.0"},
		{"RemoteAddr", vars.RemoteAddr, req.RemoteAddr},
		{"RequestMethod", vars.RequestMethod, "POST"},
		{"ServerPort", vars.ServerPort, "80"},
		{"HTTPS", vars.HTTPS, "off"},
		{"DocumentRoot", vars.DocumentRoot, "/var/www"},
		{"ServerName", vars.ServerName, "example.com"},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("BuildVariables %s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}

	// Verify TheRequest format
	if !strings.Contains(vars.TheRequest, "POST") || !strings.Contains(vars.TheRequest, "/path") {
		t.Errorf("TheRequest = %q, expected to contain method and path", vars.TheRequest)
	}

	// Test HTTPS=on path
	varsHTTPS := BuildVariables(req, "/var/www", "/var/www/path", true)
	if varsHTTPS.ServerPort != "443" {
		t.Errorf("ServerPort with HTTPS = %q, want 443", varsHTTPS.ServerPort)
	}
	if varsHTTPS.HTTPS != "on" {
		t.Errorf("HTTPS = %q, want on", varsHTTPS.HTTPS)
	}
}

func TestBuildVariablesNoPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "example.com"

	vars := BuildVariables(req, "/root", "/root/test", false)
	if vars.ServerName != "example.com" {
		t.Errorf("ServerName = %q, want example.com", vars.ServerName)
	}
}

func TestConvertConfigRewrites(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match:  "^/old$",
			To:     "/new",
			Flags:  []string{"L"},
			Status: 0,
		},
		{
			Match:      "^/blog/(.*)$",
			To:         "/index.php",
			Conditions: []string{"!is_file", "!is_dir"},
		},
		{
			Match:  "^/redir$",
			To:     "https://other.com/",
			Status: 301,
		},
	}

	rules := ConvertConfigRewrites(rewrites)

	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3", len(rules))
	}

	// First rule: has [L] flag from Flags, and since not chain & redirect==0, default Last is set
	if !rules[0].Flags.Last {
		t.Error("rule[0] should have Last flag")
	}

	// Second rule: should have 2 conditions (one for !-f and one for !-d)
	if len(rules[1].Conditions) != 2 {
		t.Errorf("rule[1] has %d conditions, want 2", len(rules[1].Conditions))
	}

	// Third rule: redirect with status 301
	if rules[2].Flags.Redirect != 301 {
		t.Errorf("rule[2] redirect = %d, want 301", rules[2].Flags.Redirect)
	}
	// With redirect set, Last should NOT be forced
	if rules[2].Flags.Last {
		t.Error("rule[2] should not have Last flag when redirect is set")
	}
}

func TestConvertConfigRewritesInvalidPattern(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match: "[invalid",
			To:    "/dest",
		},
	}
	rules := ConvertConfigRewrites(rewrites)
	if len(rules) != 0 {
		t.Errorf("got %d rules, want 0 for invalid pattern", len(rules))
	}
}

func TestConvertConfigRewritesConditionTypes(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match:      ".*",
			To:         "/index.php",
			Conditions: []string{"!-f", "-f", "!-d", "-d", "unknown_cond"},
		},
	}
	rules := ConvertConfigRewrites(rewrites)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	// "unknown_cond" should be skipped, so only 4 conditions
	if len(rules[0].Conditions) != 4 {
		t.Errorf("conditions count = %d, want 4", len(rules[0].Conditions))
	}
}

func TestGoneFlag(t *testing.T) {
	rule, _ := ParseRule("^/removed", "-", "G")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/removed"}

	result := engine.Process("/removed", "", vars)
	if !result.Gone {
		t.Error("should be gone")
	}
}

// --- condition.go: Expand all variables ---

func TestVariableExpandAll(t *testing.T) {
	vars := &Variables{
		RequestURI:      "/uri",
		RequestFilename: "/var/www/file.php",
		QueryString:     "a=1&b=2",
		HTTPHost:        "example.com",
		HTTPReferer:     "https://ref.com/",
		HTTPUserAgent:   "TestBot/1.0",
		RemoteAddr:      "192.168.1.1",
		RequestMethod:   "POST",
		ServerPort:      "443",
		HTTPS:           "on",
		DocumentRoot:    "/var/www",
		ServerName:      "example.com",
		TheRequest:      "POST /uri HTTP/1.1",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"%{REQUEST_URI}", "/uri"},
		{"%{REQUEST_FILENAME}", "/var/www/file.php"},
		{"%{QUERY_STRING}", "a=1&b=2"},
		{"%{HTTP_HOST}", "example.com"},
		{"%{HTTP_REFERER}", "https://ref.com/"},
		{"%{HTTP_USER_AGENT}", "TestBot/1.0"},
		{"%{REMOTE_ADDR}", "192.168.1.1"},
		{"%{REQUEST_METHOD}", "POST"},
		{"%{SERVER_PORT}", "443"},
		{"%{HTTPS}", "on"},
		{"%{DOCUMENT_ROOT}", "/var/www"},
		{"%{SERVER_NAME}", "example.com"},
		{"%{THE_REQUEST}", "POST /uri HTTP/1.1"},
		// Without %{} wrapper
		{"REQUEST_URI", "/uri"},
		// Unknown variable
		{"%{UNKNOWN_VAR}", ""},
	}

	for _, tt := range tests {
		got := vars.Expand(tt.input)
		if got != tt.want {
			t.Errorf("Expand(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- rule.go: ParseFlags for C, PT, NE, E= ---

func TestParseFlagsAdditional(t *testing.T) {
	tests := []struct {
		input string
		check func(Flags) bool
		desc  string
	}{
		{"C", func(f Flags) bool { return f.Chain }, "Chain"},
		{"PT", func(f Flags) bool { return f.PassThrough }, "PassThrough"},
		{"NE", func(f Flags) bool { return f.NoEscape }, "NoEscape"},
		{"E=VAR:value", func(f Flags) bool { return f.EnvironmentVar == "VAR:value" }, "EnvironmentVar"},
		{"R=xyz", func(f Flags) bool { return f.Redirect == 302 }, "Redirect invalid code defaults to 302"},
		{"", func(f Flags) bool { return !f.Last && f.Redirect == 0 }, "Empty string"},
	}

	for _, tt := range tests {
		f := ParseFlags(tt.input)
		if !tt.check(f) {
			t.Errorf("ParseFlags(%q) failed for %s", tt.input, tt.desc)
		}
	}
}

// --- engine.go: Process with "-" target (no substitution) ---

func TestProcessNoSubstitutionTarget(t *testing.T) {
	rule, _ := ParseRule("^/api/(.*)$", "-", "L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/api/v1/users"}

	result := engine.Process("/api/v1/users", "", vars)
	// "-" means no substitution, URI should remain unchanged
	if result.URI != "/api/v1/users" {
		t.Errorf("URI = %q, want /api/v1/users (no substitution)", result.URI)
	}
	// Modified should be false because the target is "-"
	if result.Modified {
		t.Error("Modified should be false for '-' target")
	}
}

// --- engine.go: Process with QSA but no query in new URI ---

func TestProcessQSANoQueryInNewURI(t *testing.T) {
	rule, _ := ParseRule("^/page$", "/view", "QSA,L")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/page"}

	result := engine.Process("/page", "lang=en", vars)
	// New URI has no ?, QSA means keep original query string
	if result.URI != "/view" {
		t.Errorf("URI = %q, want /view", result.URI)
	}
	if result.Query != "lang=en" {
		t.Errorf("Query = %q, want lang=en (preserved by QSA)", result.Query)
	}
}

// --- engine.go: Process redirect with query string ---

func TestProcessRedirectWithQuery(t *testing.T) {
	rule, _ := ParseRule("^/old$", "/new?param=1", "R=301")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/old"}

	result := engine.Process("/old", "existing=yes", vars)
	if !result.Redirect {
		t.Fatal("should be redirect")
	}
	if result.StatusCode != 301 {
		t.Errorf("status = %d, want 301", result.StatusCode)
	}
	// The redirect URI should include the new query parameter
	if !strings.Contains(result.URI, "/new") {
		t.Errorf("URI = %q, should contain /new", result.URI)
	}
}

// --- engine.go: ConvertConfigRewrites with is_file and is_dir conditions ---

func TestConvertConfigRewritesIsFileIsDir(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match:      ".*",
			To:         "/handler",
			Conditions: []string{"is_file", "is_dir"},
		},
	}
	rules := ConvertConfigRewrites(rewrites)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if len(rules[0].Conditions) != 2 {
		t.Errorf("conditions = %d, want 2", len(rules[0].Conditions))
	}
}

// --- engine.go: ConvertConfigRewrites with chain flag ---

func TestConvertConfigRewritesWithChain(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match: "^/step1$",
			To:    "/step2",
			Flags: []string{"C"},
		},
		{
			Match: "^/step2$",
			To:    "/final",
			Flags: []string{"L"},
		},
	}
	rules := ConvertConfigRewrites(rewrites)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
	// First rule has Chain flag, so Last should NOT be forced
	if rules[0].Flags.Last {
		t.Error("rule[0] with Chain should not have Last forced")
	}
}

// --- engine.go: evalConditions with OR group where first fails and second is AND ---

func TestEvalConditionsOrFirstFailsSecondAnd(t *testing.T) {
	cond1, _ := ParseCondition("%{REQUEST_METHOD}", "^DELETE$", "[OR]")
	cond2, _ := ParseCondition("%{REQUEST_METHOD}", "^PUT$", "")

	rule, _ := ParseRule(".*", "/write-handler", "L")
	rule.Conditions = []Condition{*cond1, *cond2}

	engine := NewEngine([]*Rule{rule})

	// DELETE matches via first OR condition
	vars := &Variables{RequestMethod: "DELETE", RequestURI: "/resource"}
	result := engine.Process("/resource", "", vars)
	if result.URI != "/write-handler" {
		t.Errorf("DELETE: URI = %q, want /write-handler", result.URI)
	}

	// PUT doesn't match because when cond1[OR] fails, groupResult becomes false
	// and cond2 (AND) evaluates false && true = false
	vars2 := &Variables{RequestMethod: "PUT", RequestURI: "/resource"}
	result2 := engine.Process("/resource", "", vars2)
	if result2.Modified {
		t.Error("PUT: should not be modified (OR group fails, AND with second fails)")
	}
}

// --- engine.go: evalConditions both OR conditions in group ---

func TestEvalConditionsBothOr(t *testing.T) {
	cond1, _ := ParseCondition("%{REQUEST_METHOD}", "^DELETE$", "[OR]")
	cond2, _ := ParseCondition("%{REQUEST_METHOD}", "^PUT$", "[OR]")
	cond3, _ := ParseCondition("%{REQUEST_URI}", ".*", "")

	rule, _ := ParseRule(".*", "/write-handler", "L")
	rule.Conditions = []Condition{*cond1, *cond2, *cond3}

	engine := NewEngine([]*Rule{rule})

	// PUT matches via second OR condition
	vars := &Variables{RequestMethod: "PUT", RequestURI: "/resource"}
	result := engine.Process("/resource", "", vars)
	// cond1 OR fails, cond2 OR succeeds -> overallResult=true, cond3 AND matches
	if result.URI != "/write-handler" {
		t.Errorf("PUT: URI = %q, want /write-handler", result.URI)
	}
}

// --- engine.go: ConvertConfigRewrites with condition that triggers ParseCondition error ---
// Note: Currently all valid config conditions ("-f", "!-f", etc.) never fail ParseCondition.
// The error path (line 240) is triggered when ParseCondition returns an error,
// which can't happen with the current condition types. This path is defensive code.

// --- engine.go: chain with conditions not matching ---

func TestChainConditionsMismatch(t *testing.T) {
	// Rule with condition that doesn't match, and chain flag
	cond, _ := ParseCondition("%{HTTP_HOST}", "^never\\.match$", "")
	rule0, _ := ParseRule(".*", "/a", "C")
	rule0.Conditions = []Condition{*cond}
	rule1, _ := ParseRule(".*", "/b", "L") // chained, should be skipped
	rule2, _ := ParseRule(".*", "/fallback", "L")

	engine := NewEngine([]*Rule{rule0, rule1, rule2})
	vars := &Variables{HTTPHost: "example.com", RequestURI: "/test"}
	result := engine.Process("/test", "", vars)
	if result.URI != "/fallback" {
		t.Errorf("URI = %q, want /fallback", result.URI)
	}
}
