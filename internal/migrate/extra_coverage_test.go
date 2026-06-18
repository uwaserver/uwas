package migrate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- validateSSHInput edge cases ---

func TestValidateSSHInputBadPort(t *testing.T) {
	cases := []string{"", "abc", "0", "70000", "-1"}
	for _, p := range cases {
		err := validateSSHInput(MigrateRequest{
			SourcePort: p,
			SourceHost: "user@host",
		})
		if err == nil || !strings.Contains(err.Error(), "source_port") {
			t.Errorf("port %q: expected source_port error, got %v", p, err)
		}
	}
}

func TestValidateSSHInputBadHost(t *testing.T) {
	cases := []string{"", "-flag", "has space", "bad$char", "user@-evil"}
	for _, h := range cases {
		err := validateSSHInput(MigrateRequest{
			SourcePort: "22",
			SourceHost: h,
		})
		if err == nil || !strings.Contains(err.Error(), "source_host") {
			t.Errorf("host %q: expected source_host error, got %v", h, err)
		}
	}
}

func TestValidateSSHInputBadSSHKey(t *testing.T) {
	cases := []string{
		"relative/path/key",    // not absolute
		"/path/with space/key", // contains space
		"/path/with\tkey",      // contains tab
		"/path/-leadingdash",   // base starts with dash
	}
	for _, k := range cases {
		err := validateSSHInput(MigrateRequest{
			SourcePort: "22",
			SourceHost: "user@host",
			SSHKey:     k,
		})
		if err == nil || !strings.Contains(err.Error(), "ssh_key") {
			t.Errorf("ssh_key %q: expected ssh_key error, got %v", k, err)
		}
	}
}

func TestValidateSSHInputGoodSSHKey(t *testing.T) {
	err := validateSSHInput(MigrateRequest{
		SourcePort: "22",
		SourceHost: "user@host",
		SSHKey:     "/home/user/.ssh/id_rsa",
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestValidateSSHInputBadSourcePath(t *testing.T) {
	cases := []string{
		"-rf",          // leading dash
		"path\nwith",   // newline
		"path\rwith",   // carriage return
		"path\x00null", // null byte
	}
	for _, p := range cases {
		err := validateSSHInput(MigrateRequest{
			SourcePort: "22",
			SourceHost: "user@host",
			SourcePath: p,
		})
		if err == nil || !strings.Contains(err.Error(), "source_path") {
			t.Errorf("source_path %q: expected source_path error, got %v", p, err)
		}
	}
}

func TestValidateSSHInputAllValid(t *testing.T) {
	err := validateSSHInput(MigrateRequest{
		SourcePort: "2222",
		SourceHost: "deploy@example.com",
		SourcePath: "/var/www/example.com",
		SSHKey:     "/etc/uwas/id_ed25519",
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// --- shellQuote ---

func TestShellQuoteEmpty(t *testing.T) {
	if got := shellQuote(""); got != "''" {
		t.Errorf("shellQuote(\"\") = %q, want ''", got)
	}
}

func TestShellQuotePlain(t *testing.T) {
	if got := shellQuote("simple"); got != "'simple'" {
		t.Errorf("shellQuote(simple) = %q, want 'simple'", got)
	}
}

func TestShellQuoteWithSingleQuote(t *testing.T) {
	got := shellQuote("a'b")
	want := `'a'\''b'`
	if got != want {
		t.Errorf("shellQuote(a'b) = %q, want %q", got, want)
	}
}

// --- readCPanelUserdata ---

func TestReadCPanelUserdataMissing(t *testing.T) {
	if got := readCPanelUserdata(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("missing file = %q, want empty", got)
	}
}

func TestReadCPanelUserdataMainDomainRegex(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main")
	os.WriteFile(p, []byte("user: bob\nmain_domain: example.com\n"), 0644)
	if got := readCPanelUserdata(p); got != "example.com" {
		t.Errorf("got %q, want example.com", got)
	}
}

func TestReadCPanelUserdataFallbackFirstLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main")
	os.WriteFile(p, []byte("# comment line\n\nfallback.example.org\n"), 0644)
	if got := readCPanelUserdata(p); got != "fallback.example.org" {
		t.Errorf("got %q, want fallback.example.org", got)
	}
}

func TestReadCPanelUserdataEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main")
	os.WriteFile(p, []byte("# only comments\n\n"), 0644)
	if got := readCPanelUserdata(p); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- readCPanelDocRoot ---

func TestReadCPanelDocRootMissing(t *testing.T) {
	if got := readCPanelDocRoot(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("missing = %q, want empty", got)
	}
}

func TestReadCPanelDocRootPublicHTML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dom")
	os.WriteFile(p, []byte("documentroot: /home/bob/public_html/addon\n"), 0644)
	if got := readCPanelDocRoot(p); got != "public_html/addon" {
		t.Errorf("got %q, want public_html/addon", got)
	}
}

func TestReadCPanelDocRootLastTwoComponents(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dom")
	os.WriteFile(p, []byte("documentroot: /home/bob/sites/myapp\n"), 0644)
	if got := readCPanelDocRoot(p); got != "sites/myapp" {
		t.Errorf("got %q, want sites/myapp", got)
	}
}

func TestReadCPanelDocRootBaseOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dom")
	os.WriteFile(p, []byte("documentroot: webroot\n"), 0644)
	if got := readCPanelDocRoot(p); got != "webroot" {
		t.Errorf("got %q, want webroot", got)
	}
}

func TestReadCPanelDocRootNoDirective(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dom")
	os.WriteFile(p, []byte("something: else\n"), 0644)
	if got := readCPanelDocRoot(p); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- parseCPanelDomains ---

func TestParseCPanelDomainsFull(t *testing.T) {
	cpRoot := t.TempDir()
	userdata := filepath.Join(cpRoot, "userdata")
	os.MkdirAll(userdata, 0755)

	// main domain file
	os.WriteFile(filepath.Join(userdata, "main"), []byte("main_domain: example.com\n"), 0644)
	// addon domain with explicit docroot
	os.WriteFile(filepath.Join(userdata, "addon.com"),
		[]byte("documentroot: /home/bob/public_html/addon.com\n"), 0644)
	// subdomain (more than one dot) without docroot -> default
	os.WriteFile(filepath.Join(userdata, "sub.test.com"), []byte("nothing\n"), 0644)
	// files that must be skipped
	os.WriteFile(filepath.Join(userdata, "example.com_SSL"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(userdata, "foo.cache"), []byte("x"), 0644)
	os.MkdirAll(filepath.Join(userdata, "adir"), 0755)
	// duplicate of main domain - should be skipped via seen map
	os.WriteFile(filepath.Join(userdata, "example.com"), []byte("x"), 0644)

	domains := parseCPanelDomains(cpRoot)

	byName := map[string]CPanelDomain{}
	for _, d := range domains {
		byName[d.Domain] = d
	}

	if d, ok := byName["example.com"]; !ok || d.Type != "main" {
		t.Errorf("expected main example.com, got %+v / domains=%+v", d, domains)
	}
	if d, ok := byName["addon.com"]; !ok || d.Type != "addon" || d.DocRoot != "public_html/addon.com" {
		t.Errorf("addon.com wrong: %+v", d)
	}
	if d, ok := byName["sub.test.com"]; !ok || d.Type != "sub" || d.DocRoot != "public_html/sub.test.com" {
		t.Errorf("sub.test.com wrong: %+v", d)
	}
	if _, ok := byName["example.com_SSL"]; ok {
		t.Error("_SSL file should be skipped")
	}
	if _, ok := byName["foo.cache"]; ok {
		t.Error(".cache file should be skipped")
	}
	if _, ok := byName["adir"]; ok {
		t.Error("directory should be skipped")
	}
}

func TestParseCPanelDomainsNoUserdataWithPublicHTML(t *testing.T) {
	cpRoot := t.TempDir()
	// no userdata dir; but homedir/public_html exists -> "unknown" main domain
	os.MkdirAll(filepath.Join(cpRoot, "homedir", "public_html"), 0755)

	domains := parseCPanelDomains(cpRoot)
	if len(domains) != 1 || domains[0].Domain != "unknown" || domains[0].Type != "main" {
		t.Errorf("expected single unknown main domain, got %+v", domains)
	}
}

func TestParseCPanelDomainsNoUserdataNoPublicHTML(t *testing.T) {
	cpRoot := t.TempDir()
	domains := parseCPanelDomains(cpRoot)
	if len(domains) != 0 {
		t.Errorf("expected no domains, got %+v", domains)
	}
}

// --- ImportCPanelBackup ---

// writeCPanelBackup builds a gzip-compressed tar archive at path containing the
// given files (map of archive-relative path -> content). Directory entries are
// created implicitly. A trailing slash in the name marks a directory entry.
func writeCPanelBackup(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, content := range files {
		if strings.HasSuffix(name, "/") {
			hdr := &tar.Header{
				Name:     name,
				Typeflag: tar.TypeDir,
				Mode:     0755,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatal(err)
			}
			continue
		}
		hdr := &tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0644,
			Size:     int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestImportCPanelBackupOpenError(t *testing.T) {
	_, err := ImportCPanelBackup(filepath.Join(t.TempDir(), "missing.tar.gz"), t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "open backup") {
		t.Errorf("expected open error, got %v", err)
	}
}

func TestImportCPanelBackupNotGzip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.tar.gz")
	os.WriteFile(p, []byte("this is not gzip data at all"), 0644)
	_, err := ImportCPanelBackup(p, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "gzip reader") {
		t.Errorf("expected gzip error, got %v", err)
	}
}

func TestImportCPanelBackupNoRootDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	// Only a top-level regular file, no directory -> no cPanel root dir
	writeCPanelBackup(t, p, map[string]string{
		"loosefile.txt": "data",
	})
	_, err := ImportCPanelBackup(p, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "no cPanel root directory") {
		t.Errorf("expected no root dir error, got %v", err)
	}
}

func TestImportCPanelBackupTraversalSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	target := t.TempDir()
	// path traversal entry is skipped; a valid cpmove dir still present
	writeCPanelBackup(t, p, map[string]string{
		"../evil.txt":                              "pwn",
		"cpmove-bob/":                              "",
		"cpmove-bob/homedir/":                      "",
		"cpmove-bob/homedir/public_html/":          "",
		"cpmove-bob/homedir/public_html/index.php": "<?php echo 1; ?>",
		"cpmove-bob/userdata/main":                 "main_domain: bob.com\n",
	})
	res, err := ImportCPanelBackup(p, target, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.User != "bob" {
		t.Errorf("user = %q, want bob", res.User)
	}
	if len(res.Domains) != 1 || res.Domains[0].Domain != "bob.com" {
		t.Errorf("domains = %+v, want bob.com", res.Domains)
	}
	// index.php should have been copied to target/bob.com/public_html
	if _, err := os.Stat(filepath.Join(target, "bob.com", "public_html", "index.php")); err != nil {
		t.Errorf("expected copied index.php: %v", err)
	}
}

func TestImportCPanelBackupFullWithSSLAndDB(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	target := t.TempDir()
	writeCPanelBackup(t, p, map[string]string{
		"cpmove-alice/":                           "",
		"cpmove-alice/homedir/":                   "",
		"cpmove-alice/homedir/public_html/":       "",
		"cpmove-alice/homedir/public_html/i.html": "<html></html>",
		"cpmove-alice/userdata/main":              "main_domain: alice.com\n",
		"cpmove-alice/ssl/alice.com.crt":          "CERTDATA",
		"cpmove-alice/ssl/alice.com.key":          "KEYDATA",
		"cpmove-alice/mysql/alice_wp.sql":         "CREATE TABLE x;",
		"cpmove-alice/mysql/notes.txt":            "ignore me",
	})
	res, err := ImportCPanelBackup(p, target, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.User != "alice" {
		t.Errorf("user = %q, want alice", res.User)
	}
	if res.SSLCerts != 1 {
		t.Errorf("SSLCerts = %d, want 1", res.SSLCerts)
	}
	if len(res.Domains) != 1 || !res.Domains[0].SSL {
		t.Errorf("domain SSL not set: %+v", res.Domains)
	}
	// cert + key copied
	if _, err := os.Stat(filepath.Join(target, ".certs", "alice.com", "cert.pem")); err != nil {
		t.Errorf("cert.pem missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".certs", "alice.com", "key.pem")); err != nil {
		t.Errorf("key.pem missing: %v", err)
	}
	// only the .sql file is discovered as a database
	if len(res.Databases) != 1 || res.Databases[0].Name != "alice_wp" {
		t.Errorf("databases = %+v, want one alice_wp", res.Databases)
	}
	if res.Databases[0].User != "alice" {
		t.Errorf("db user = %q, want alice", res.Databases[0].User)
	}
}

func TestImportCPanelBackupNoHomedirFallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	target := t.TempDir()
	// No homedir subdir; public_html directly under cpRoot. DocRoot won't be
	// found, so the addon fallback / "no files" branch runs.
	writeCPanelBackup(t, p, map[string]string{
		"cpmove-carl/":                  "",
		"cpmove-carl/public_html/":      "",
		"cpmove-carl/public_html/a.txt": "hi",
		"cpmove-carl/userdata/main":     "main_domain: carl.com\n",
	})
	_, err := ImportCPanelBackup(p, target, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// main docroot is "public_html"; homedir falls back to cpRoot, so
	// cpRoot/public_html exists and files copy.
	if _, err := os.Stat(filepath.Join(target, "carl.com", "public_html", "a.txt")); err != nil {
		t.Errorf("expected copied a.txt: %v", err)
	}
}

func TestImportCPanelBackupMissingFilesError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	target := t.TempDir()
	// Domain declared but no actual files anywhere -> "no files for" error.
	writeCPanelBackup(t, p, map[string]string{
		"cpmove-dan/":              "",
		"cpmove-dan/userdata/main": "main_domain: dan.com\n",
	})
	res, err := ImportCPanelBackup(p, target, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, "no files for dan.com") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'no files for' error, got %+v", res.Errors)
	}
}

func TestImportCPanelBackupTarReadError(t *testing.T) {
	// Valid gzip stream but truncated/corrupt tar content -> tr.Next() errors.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte("not a valid tar header block, just random bytes that gzip wraps"))
	gz.Close()
	p := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	os.WriteFile(p, buf.Bytes(), 0644)

	_, err := ImportCPanelBackup(p, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "read tar") {
		t.Errorf("expected read tar error, got %v", err)
	}
}

func TestImportCPanelBackupAddonDocRoot(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	target := t.TempDir()
	// addon domain whose docroot is missing at the primary location triggers the
	// addon fallback branch (srcRoot = homedir/DocRoot). The files live there.
	writeCPanelBackup(t, p, map[string]string{
		"cpmove-eve/":                               "",
		"cpmove-eve/homedir/":                       "",
		"cpmove-eve/homedir/public_html/":           "",
		"cpmove-eve/homedir/public_html/index.html": "main",
		"cpmove-eve/homedir/addons/shop/":           "",
		"cpmove-eve/homedir/addons/shop/i.php":      "<?php",
		"cpmove-eve/userdata/main":                  "main_domain: eve.com\n",
		"cpmove-eve/userdata/shop.eve.io":           "documentroot: /home/eve/addons/shop\n",
	})
	res, err := ImportCPanelBackup(p, target, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// shop.eve.io has two dots -> classified "sub", docroot "addons/shop"
	var shop *CPanelDomain
	for i := range res.Domains {
		if res.Domains[i].Domain == "shop.eve.io" {
			shop = &res.Domains[i]
		}
	}
	if shop == nil {
		t.Fatalf("shop.eve.io not parsed: %+v", res.Domains)
	}
	if _, err := os.Stat(filepath.Join(target, "shop.eve.io", "public_html", "i.php")); err != nil {
		t.Errorf("expected copied addon file: %v", err)
	}
}

func TestImportCPanelBackupAddonTypeFallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	target := t.TempDir()
	// addon.io has a single dot -> classified "addon". Its docroot points to a
	// path that does NOT exist at homedir/<docroot>, so the addon-specific
	// fallback branch (srcRoot = homedir/dom.DocRoot) executes. Files are then
	// absent -> "no files for" error, but the branch is covered.
	writeCPanelBackup(t, p, map[string]string{
		"cpmove-fred/":                               "",
		"cpmove-fred/homedir/":                       "",
		"cpmove-fred/homedir/public_html/":           "",
		"cpmove-fred/homedir/public_html/index.html": "main",
		"cpmove-fred/userdata/main":                  "main_domain: fred.com\n",
		"cpmove-fred/userdata/addon.io":              "documentroot: /home/fred/missing/spot\n",
	})
	res, err := ImportCPanelBackup(p, target, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var addon *CPanelDomain
	for i := range res.Domains {
		if res.Domains[i].Domain == "addon.io" {
			addon = &res.Domains[i]
		}
	}
	if addon == nil || addon.Type != "addon" {
		t.Fatalf("expected addon.io type addon, got %+v", res.Domains)
	}
}

// --- copyFile error path ---

func TestCopyFileCreateError(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	os.WriteFile(src, []byte("data"), 0644)
	// destination directory does not exist -> os.Create fails
	dst := filepath.Join(t.TempDir(), "no-such-dir", "out.txt")
	if err := copyFile(src, dst); err == nil {
		t.Error("expected error creating dst in missing directory")
	}
}

// --- Clone invalid identifier branches ---

func cloneStubs(t *testing.T) {
	t.Helper()
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	t.Cleanup(func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	})
	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error { return nil }
	runCloneChown = func(root string) {}
}

func TestCloneInvalidSourceDB(t *testing.T) {
	cloneStubs(t)
	res := Clone(CloneRequest{
		SourceDomain: "a.com",
		TargetDomain: "b.com",
		SourceRoot:   t.TempDir(),
		TargetRoot:   t.TempDir(),
		SourceDB:     "bad name;drop",
		TargetDB:     "valid_db",
	})
	if res.Status != "error" || !strings.Contains(res.Error, "invalid source database") {
		t.Errorf("status=%q err=%q, want invalid source database", res.Status, res.Error)
	}
}

func TestCloneInvalidTargetDB(t *testing.T) {
	cloneStubs(t)
	res := Clone(CloneRequest{
		SourceDomain: "a.com",
		TargetDomain: "b.com",
		SourceRoot:   t.TempDir(),
		TargetRoot:   t.TempDir(),
		SourceDB:     "valid_src",
		TargetDB:     "bad;name",
	})
	if res.Status != "error" || !strings.Contains(res.Error, "invalid target database") {
		t.Errorf("status=%q err=%q, want invalid target database", res.Status, res.Error)
	}
}

func TestCloneInvalidDBUser(t *testing.T) {
	cloneStubs(t)
	res := Clone(CloneRequest{
		SourceDomain: "a.com",
		TargetDomain: "b.com",
		SourceRoot:   t.TempDir(),
		TargetRoot:   t.TempDir(),
		SourceDB:     "valid_src",
		TargetDB:     "valid_dst",
		DBUser:       "bad;user",
	})
	if res.Status != "error" || !strings.Contains(res.Error, "invalid database user") {
		t.Errorf("status=%q err=%q, want invalid database user", res.Status, res.Error)
	}
}

// --- Migrate: validateSSHInput failure path ---

func TestMigrateDBRealInvalidDBUser(t *testing.T) {
	var log strings.Builder
	res := migrateDBReal(MigrateRequest{
		SourceHost: "user@host",
		SourcePort: "22",
		DBName:     "validdb",
		DBUser:     "bad;user",
	}, &log)
	if res != "error: invalid database user" {
		t.Errorf("res = %q, want invalid database user", res)
	}
}

func TestMigrateInvalidSSHInput(t *testing.T) {
	res := Migrate(MigrateRequest{
		SourceHost: "user@host",
		LocalRoot:  t.TempDir(),
		SourcePort: "not-a-port",
	})
	if res.Status != "error" || !strings.Contains(res.Error, "invalid source_port") {
		t.Errorf("status=%q err=%q, want invalid source_port", res.Status, res.Error)
	}
}
