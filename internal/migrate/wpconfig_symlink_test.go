package migrate

// F45300 / F45301: the clone and site-migration steps run as root and rewrite
// wp-config.php inside a tenant-writable tree. rsync -a copies a planted
// symlink as a symlink, so os.ReadFile/os.Chmod/os.WriteFile on that path used
// to read, chmod and rewrite whatever the link pointed at (another tenant's
// wp-config.php, any PHP file on the host). A symlink must be left alone.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const victimConfig = "<?php\ndefine('DB_NAME', 'victim_db');\ndefine('DB_USER', 'victim_user');\ndefine('DB_PASSWORD', 'victim_pw');\ndefine('DB_HOST', 'victim.example');\nrequire_once ABSPATH . 'wp-settings.php';\n"

// plantedSymlink returns the symlink path and the victim file it points at.
func plantedSymlink(t *testing.T) (link, victim string) {
	t.Helper()
	dir := t.TempDir()
	victim = filepath.Join(dir, "other-tenant", "wp-config.php")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte(victimConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(dir, "clone", "wp-config.php")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return link, victim
}

func assertVictimUntouched(t *testing.T, victim string) {
	t.Helper()
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: victim unchanged, mode 0644\nACTUAL: mode %04o, changed=%v", fi.Mode().Perm(), string(got) != victimConfig)
	if string(got) != victimConfig {
		t.Errorf("PROBLEM CONFIRMED: the file behind the planted wp-config.php symlink was rewritten:\n%s", got)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("PROBLEM CONFIRMED: the file behind the symlink was chmod'ed to %04o, want 0644", fi.Mode().Perm())
	}
}

func TestUpdateWPConfigURLsLeavesSymlinkTargetAlone(t *testing.T) {
	link, victim := plantedSymlink(t)
	var log strings.Builder
	updateWPConfigURLs(link, "clone.example.com", &log)
	assertVictimUntouched(t, victim)
	if !strings.Contains(log.String(), "symlink") {
		t.Errorf("log = %q, want a note that wp-config.php is a symlink", log.String())
	}
}

func TestUpdateWPConfigDBLeavesSymlinkTargetAlone(t *testing.T) {
	link, victim := plantedSymlink(t)
	var log strings.Builder
	updateWPConfigDB(link, "clone_db", "clone_user", "clone_pw", &log)
	assertVictimUntouched(t, victim)
	if !strings.Contains(log.String(), "symlink") {
		t.Errorf("log = %q, want a note that wp-config.php is a symlink", log.String())
	}
}

// Controls: an ordinary wp-config.php is still rewritten (and kept 0600).
func TestUpdateWPConfigRegularFileStillRewritten(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wp-config.php")
	if err := os.WriteFile(p, []byte(victimConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	updateWPConfigURLs(p, "clone.example.com", &log)
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), "WP_HOME', 'https://clone.example.com'") {
		t.Fatalf("control: WP_HOME not written:\n%s", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("control: mode = %04o, want 0600", fi.Mode().Perm())
	}

	p2 := filepath.Join(dir, "wp-config2.php")
	if err := os.WriteFile(p2, []byte(victimConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	updateWPConfigDB(p2, "clone_db", "clone_user", "clone_pw", &log)
	got, _ = os.ReadFile(p2)
	if !strings.Contains(string(got), "define('DB_NAME', 'clone_db');") || strings.Contains(string(got), "victim_db") {
		t.Errorf("control: DB credentials not rewritten:\n%s", got)
	}
}
