package rewrite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConditionTestStringSeesRuleBackrefs pins Apache's RewriteCond TestString
// backreferences: "$N" names the RewriteRule's captures, so the standard
// "serve the .webp if it exists" block works. Conditions used to be evaluated
// before the rule's pattern with only %{NAME} expanded, so "$1" was tested
// literally and the rule never fired (F68000).
func TestConditionTestStringSeesRuleBackrefs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"img/a.jpg", "img/a.webp"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newEngine := func() *Engine {
		cond, err := ParseCondition("%{DOCUMENT_ROOT}/$1.webp", "-f", "")
		if err != nil {
			t.Fatal(err)
		}
		rule, err := ParseRule(`^/?(.+)\.(jpe?g|png)$`, "/$1.webp", "NC,L")
		if err != nil {
			t.Fatal(err)
		}
		rule.Conditions = []Condition{*cond}
		return NewEngine([]*Rule{rule})
	}
	vars := func(uri string) *Variables {
		return &Variables{RequestURI: uri, DocumentRoot: root, RequestFilename: filepath.Join(root, uri)}
	}

	if r := newEngine().Process("/img/a.jpg", "", vars("/img/a.jpg")); !r.Modified || r.URI != "/img/a.webp" {
		t.Errorf("existing .webp: URI=%q modified=%v, want /img/a.webp", r.URI, r.Modified)
	}
	if r := newEngine().Process("/img/b.jpg", "", vars("/img/b.jpg")); r.Modified {
		t.Errorf("missing .webp: rewritten to %q, want no rewrite", r.URI)
	}

	// A captured value is substituted once: "%{HTTP_HOST}" text in the path
	// stays literal.
	cond, err := ParseCondition("$1", "=a%{HTTP_HOST}", "")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := ParseRule(`^/(.+)\.jpg$`, "/hit", "L")
	if err != nil {
		t.Fatal(err)
	}
	rule.Conditions = []Condition{*cond}
	v := &Variables{RequestURI: "/a%{HTTP_HOST}.jpg", HTTPHost: "example.com"}
	if r := NewEngine([]*Rule{rule}).Process("/a%{HTTP_HOST}.jpg", "", v); r.URI != "/hit" {
		t.Errorf("captured %%{HTTP_HOST} text: URI=%q, want /hit", r.URI)
	}
}

// TestConditionTestStringSeesConditionBackrefs pins %N in a RewriteCond
// TestString: the captures of the last matched condition, as in Apache's
// per-host directory block. "%2" used to be tested literally (F68001).
func TestConditionTestStringSeesConditionBackrefs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sites", "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sites", "example.com", "page.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	host, err := ParseCondition("%{HTTP_HOST}", `^(www\.)?(.+)$`, "")
	if err != nil {
		t.Fatal(err)
	}
	file, err := ParseCondition("%{DOCUMENT_ROOT}/sites/%2%{REQUEST_URI}", "-f", "")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := ParseRule(`^/(.*)$`, "/sites/%2/$1", "L")
	if err != nil {
		t.Fatal(err)
	}
	rule.Conditions = []Condition{*host, *file}

	for host, want := range map[string]string{
		"www.example.com": "/sites/example.com/page.html",
		"example.com":     "/sites/example.com/page.html",
		"other.org":       "/page.html", // no such directory: not rewritten
	} {
		v := &Variables{RequestURI: "/page.html", HTTPHost: host, DocumentRoot: root}
		if r := NewEngine([]*Rule{rule}).Process("/page.html", "", v); r.URI != want {
			t.Errorf("host %s: URI=%q, want %q", host, r.URI, want)
		}
	}
}
