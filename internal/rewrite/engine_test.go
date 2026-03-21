package rewrite

import (
	"os"
	"path/filepath"
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
	rule2, _ := ParseRule(".*", "/public", "L")   // this should be skipped
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
