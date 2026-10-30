package migrate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImportCPanelBackupKeyOwnerOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	target := t.TempDir()
	// A certificate that does not parse is no longer imported, so the fixture
	// carries a real pair.
	certPEM, keyPEM := selfSignedPair(t, []string{"alice.com"}, time.Now().Add(90*24*time.Hour))
	writeCPanelBackup(t, p, map[string]string{
		"cpmove-alice/":                           "",
		"cpmove-alice/homedir/":                   "",
		"cpmove-alice/homedir/public_html/":       "",
		"cpmove-alice/homedir/public_html/i.html": "<html></html>",
		"cpmove-alice/userdata/main":              "main_domain: alice.com\n",
		"cpmove-alice/ssl/alice.com.crt":          certPEM,
		"cpmove-alice/ssl/alice.com.key":          keyPEM,
	})
	// operator pre-created .certs world-readable (MkdirAll won't tighten it)
	if err := os.MkdirAll(filepath.Join(target, ".certs"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportCPanelBackup(p, target, false); err != nil {
		t.Fatal(err)
	}
	// control: cert may be 0644
	_, err := os.Stat(filepath.Join(target, ".certs", "alice.com", "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ki, err := os.Stat(filepath.Join(target, ".certs", "alice.com", "key.pem"))
	if err != nil {
		t.Fatal(err)
	}

	if ki.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key.pem mode %v, want 0600", ki.Mode().Perm())
	}
}
