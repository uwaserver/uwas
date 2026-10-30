package migrate

import (
	"strings"
	"testing"
)

// F54602: a `return` inside a server-level `if` block was read as the
// server's own return, turning the whole site into a redirect to itself.
// F54603: a regex server_name ("~^…") was written as the host / an alias,
// where it can never match. F54604: a server-level `include` was dropped
// without a warning.

func TestNginxIfBlockDoesNotBecomeServerReturn(t *testing.T) {
	src := "server {\n listen 80;\n server_name c.com;\n root /srv/c;\n if ($host != c.com) {\n  return 301 https://c.com$request_uri;\n }\n location / {\n  try_files $uri /index.html;\n }\n}\n"
	y, warns, err := NginxToYAMLWithWarnings(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(y, "type: redirect") || strings.Contains(y, "redirect:") {
		t.Fatalf("an if-guarded return became the site's redirect:\n%s", y)
	}
	if !strings.Contains(y, "root: /srv/c") {
		t.Fatalf("site root lost:\n%s", y)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "if") {
		t.Fatalf("no warning that the if block was not converted: %v", warns)
	}
}

func TestNginxIfBlockOneLine(t *testing.T) {
	src := "server { listen 80; server_name c.com; root /srv/c; if ($a) { return 301 https://x/; } }"
	y, _, err := NginxToYAMLWithWarnings(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(y, "redirect:") {
		t.Fatalf("one-line if block became a redirect:\n%s", y)
	}
}

func TestNginxPlainServerReturnStillRedirects(t *testing.T) {
	src := "server {\n listen 80;\n server_name c.com;\n return 301 https://c.com$request_uri;\n}\n"
	y, _, err := NginxToYAMLWithWarnings(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(y, "type: redirect") {
		t.Fatalf("control: a plain server return must still convert:\n%s", y)
	}
}

func TestNginxRegexServerNameSkippedWithWarning(t *testing.T) {
	src := "server {\n listen 80;\n server_name ~^(www\\.)?h\\.com$ i.com;\n root /srv/h;\n}\n"
	y, warns, err := NginxToYAMLWithWarnings(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(y, "~^") {
		t.Fatalf("regex server_name written into the config:\n%s", y)
	}
	if !strings.Contains(y, "host: i.com") {
		t.Fatalf("literal name must become the host:\n%s", y)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "regex") {
		t.Fatalf("no warning about the skipped regex name: %v", warns)
	}
}

func TestNginxServerIncludeWarns(t *testing.T) {
	src := "server {\n listen 80;\n server_name d.com;\n include /etc/nginx/snippets/x.conf;\n root /srv/d;\n}\n"
	_, warns, err := NginxToYAMLWithWarnings(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "include") {
		t.Fatalf("no warning that the include was not followed: %v", warns)
	}
}
