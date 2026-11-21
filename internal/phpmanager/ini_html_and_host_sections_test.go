package phpmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// php-cgi and php-fpm print `-i` as HTML table rows; the loaded php.ini must
// still be recognised (F65000).
func TestParseConfigPathHTMLPHPInfo(t *testing.T) {
	info := "Content-type: text/html; charset=UTF-8\r\n\r\n<table>\n" +
		`<tr><td class="e">Configuration File (php.ini) Path </td><td class="v">/usr/local/etc/php </td></tr>` + "\n" +
		`<tr><td class="e">Loaded Configuration File </td><td class="v">/usr/local/etc/php/a&amp;b.ini </td></tr>` + "\n" +
		"</table>\n"
	if got := parseConfigPath(info); got != "/usr/local/etc/php/a&b.ini" {
		t.Fatalf("parseConfigPath(html) = %q, want /usr/local/etc/php/a&b.ini", got)
	}
	none := `<tr><td class="e">Loaded Configuration File </td><td class="v">(none) </td></tr>`
	if got := parseConfigPath(none); got != "" {
		t.Fatalf("parseConfigPath(html none) = %q, want empty", got)
	}

	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	if err := os.WriteFile(ini, []byte("[PHP]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan := `<tr><td class="e">Scan this dir for additional .ini files </td><td class="v">` + filepath.Join(dir, "conf.d") + ` </td></tr>`
	if got := findOrCreatePHPConfig("8.9.1", scan); got != ini {
		t.Fatalf("findOrCreatePHPConfig(html scan dir) = %q, want %q", got, ini)
	}
}

// Directives under [HOST=]/[PATH=] apply only to that host/path, so the
// global php.ini editor must neither change nor report them (F65001).
func TestINIHostPathSectionsAreNotGlobal(t *testing.T) {
	const base = "[PHP]\nmemory_limit = 128M\n\n[HOST=dev.example.test]\nmemory_limit = 1G\nmax_input_vars = 100\n"
	p := filepath.Join(t.TempDir(), "php.ini")
	if err := os.WriteFile(p, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateINI(p, "max_input_vars", "5000"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	want := base + "\n[PHP]\nmax_input_vars = 5000"
	if string(data) != want {
		t.Fatalf("updateINI wrote\n%q\nwant\n%q", data, want)
	}

	cfg, err := parseINIConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MemoryLimit != "128M" {
		t.Fatalf("MemoryLimit = %q, want global 128M (not the HOST value)", cfg.MemoryLimit)
	}
	if !strings.Contains(string(data), "[HOST=dev.example.test]\nmemory_limit = 1G\nmax_input_vars = 100\n") {
		t.Fatalf("HOST section changed:\n%s", data)
	}
}
