package phpmanager

// Regression guard: a per-domain override must not move UWAS's enforced
// isolation paths or name a file PHP opens at startup, where open_basedir is
// not checked. SetDomainConfig is reachable by any user who can manage the
// domain, and overrides are emitted after the enforced block (last value
// wins), so error_log = <another site's .php> wrote attacker-influenced text
// into that site's web root.

import (
	"strings"
	"testing"
)

func lastINIValue(ini, key string) (string, bool) {
	v, found := "", false
	for _, line := range strings.Split(ini, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, ";") {
			continue
		}
		if k, val, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == key {
			v, found = strings.TrimSpace(val), true
		}
	}
	return v, found
}

func TestDomainOverrideCannotEscapeIsolation(t *testing.T) {
	keys := []string{
		"error_log", "mail.log", "session.save_path", "upload_tmp_dir", "sys_temp_dir",
		"opcache.preload", "opcache.preload_user", "opcache.file_cache",
		"opcache.error_log", "opcache.lockfile_path",
	}
	const foreign = "/var/www/victim.test/public/x.php"
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			m := New(testLogger())
			m.RegisterExistingDomain("site.test", "8.4", "127.0.0.1:9001", t.TempDir(), nil)
			if err := m.SetDomainConfig("site.test", key, foreign); err == nil {
				t.Errorf("SetDomainConfig accepted %s = %s", key, foreign)
			}
			// Config-seeded overrides skip SetDomainConfig; the renderer
			// must drop them too.
			ini := renderINIFor(t, "site.test", t.TempDir(), map[string]string{key: foreign})
			if v, _ := lastINIValue(ini, key); v == foreign {
				t.Errorf("rendered ini makes %s = %s effective:\n%s", key, foreign, ini)
			}
		})
	}

	// Ordinary tuning overrides still work.
	m := New(testLogger())
	m.RegisterExistingDomain("site.test", "8.4", "127.0.0.1:9001", t.TempDir(), nil)
	if err := m.SetDomainConfig("site.test", "memory_limit", "256M"); err != nil {
		t.Fatalf("memory_limit override rejected: %v", err)
	}
}
