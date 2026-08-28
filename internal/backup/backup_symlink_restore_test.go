package backup

import (
	"os"
	"path/filepath"
	"testing"
)

// safeRestorePath compared a resolved path against an unresolved one whenever
// the destination directory did not exist yet. Any restore root reached
// through a symlink — /var/www on a second volume, a temp dir on macOS where
// /var itself is a symlink — failed the containment check, and RestoreBackup
// answered a rejected path with `continue`. The archive entry was dropped and
// the restore still reported success.
//
// These tests build the symlink explicitly so they fail on every platform,
// not only where the OS happens to supply one.

// symlinkedRoot returns (linkPath, realPath) where linkPath is a symlink to a
// real directory, mirroring a restore root that lives behind a link.
func symlinkedRoot(t *testing.T) (string, string) {
	t.Helper()

	real := filepath.Join(t.TempDir(), "gercek")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(t.TempDir(), "baglanti")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("sembolik bağlantı kurulamıyor: %v", err)
	}
	return link, real
}

// The destination directory does not exist yet — the case restore always hits
// on a fresh machine, and the one that silently dropped every entry.
func TestSafeRestorePathUnderSymlinkedRootMissingDir(t *testing.T) {
	link, _ := symlinkedRoot(t)
	base := filepath.Join(link, "domains.d") // yok

	got, ok := safeRestorePath(base, "test.yaml")
	if !ok {
		t.Fatal("sembolik bağlantı altındaki hedef reddedildi — restore girdiyi sessizce atlar")
	}
	if want := filepath.Join(base, "test.yaml"); got != want {
		t.Errorf("safeRestorePath = %q, want %q", got, want)
	}
}

func TestSafeRestorePathUnderSymlinkedRootExistingDir(t *testing.T) {
	link, real := symlinkedRoot(t)
	base := filepath.Join(link, "certs")
	if err := os.MkdirAll(filepath.Join(real, "certs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, ok := safeRestorePath(base, "sub/x.pem"); !ok {
		t.Error("var olan dizin sembolik bağlantı altındayken reddedildi")
	}
}

// The guard must still reject what it was written to reject.
func TestSafeRestorePathStillRejectsEscapes(t *testing.T) {
	link, real := symlinkedRoot(t)
	base := filepath.Join(link, "kok")
	if err := os.MkdirAll(filepath.Join(real, "kok"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Traversal out of the base.
	for _, rel := range []string{"../disari.txt", "../../disari.txt", "a/../../disari.txt"} {
		if _, ok := safeRestorePath(base, rel); ok {
			t.Errorf("%q kabul edildi — kök dışına yazılırdı", rel)
		}
	}

	// Absolute paths and empty input.
	for _, rel := range []string{"/etc/passwd", "", "."} {
		if _, ok := safeRestorePath(base, rel); ok {
			t.Errorf("%q kabul edildi", rel)
		}
	}

	// A symlink already inside the base that points outside it: writing
	// through it would land outside the restore root.
	kacis := filepath.Join(t.TempDir(), "hedef")
	if err := os.MkdirAll(kacis, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(kacis, filepath.Join(real, "kok", "kacis")); err != nil {
		t.Skipf("sembolik bağlantı kurulamıyor: %v", err)
	}
	if _, ok := safeRestorePath(base, "kacis/dosya.txt"); ok {
		t.Error("kök dışına çıkan sembolik bağlantı üzerinden yazma kabul edildi")
	}
	if _, ok := safeRestorePath(base, "kacis"); ok {
		t.Error("sembolik bağlantının kendisi hedef olarak kabul edildi")
	}
}

// End to end: a restore into a symlinked root must actually write the files.
func TestRestoreBackupIntoSymlinkedRoot(t *testing.T) {
	m, _ := testManager(t)

	src := t.TempDir()
	cfg := filepath.Join(src, "uwas.yaml")
	if err := os.WriteFile(cfg, []byte("config"), 0o644); err != nil {
		t.Fatalf("yaz: %v", err)
	}
	srcDomains := filepath.Join(src, "domains.d")
	if err := os.MkdirAll(srcDomains, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDomains, "test.yaml"), []byte("host: test.com\n"), 0o644); err != nil {
		t.Fatalf("yaz: %v", err)
	}

	m.SetPaths(cfg, "")
	m.SetDomainPaths("", srcDomains, nil)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	link, _ := symlinkedRoot(t)
	dstDomains := filepath.Join(link, "domains.d")
	m.SetPaths(filepath.Join(link, "uwas.yaml"), "")
	m.mu.Lock()
	m.domainsDir = dstDomains
	m.mu.Unlock()

	if err := m.RestoreBackup(info.Name, "mem"); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDomains, "test.yaml"))
	if err != nil {
		t.Fatalf("geri yüklenen dosya okunamadı — restore başarı bildirdi ama yazmadı: %v", err)
	}
	if string(got) != "host: test.com\n" {
		t.Errorf("içerik = %q", string(got))
	}
}
