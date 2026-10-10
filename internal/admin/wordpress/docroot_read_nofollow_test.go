package wordpress

// Regression test for F790/F791: tenant docroot files read by SiteDetail / SecurityStatus
// (and every other osReadFileFn caller) are opened without following a
// final-component symlink, never block on a FIFO, must be regular, and are
// size-bounded; regular files behave exactly as before.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	wp "github.com/uwaserver/uwas/internal/wordpress"
)

const docrootReadConfig = `<?php
define('DB_NAME', '%s');
define('DB_USER', '%s');
define('DB_HOST', '%s');
define('DISALLOW_FILE_EDIT', true);
$table_prefix = '%s';
require_once ABSPATH . 'wp-settings.php';
`

func docrootReadCall(root string, security bool) (int, map[string]any) {
	h := New(fakeDeps{root: root})
	path := "/api/v1/wordpress/sites/a.test"
	if security {
		path += "/security"
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue("domain", "a.test")
	rec := httptest.NewRecorder()
	if security {
		h.SecurityStatus(rec, req)
	} else {
		h.SiteDetail(rec, req)
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func docrootReadS(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func TestDocrootReadsDoNotFollowSymlinksOrBlock(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ok, bad := 0, 0
	check := func(name string, cond bool, detail string) {
		if cond {
			ok++
			fmt.Printf("OK       %s\n", name)
		} else {
			bad++
			fmt.Printf("MISMATCH %s: %s\n", name, detail)
		}
	}

	victim := t.TempDir()
	victimCfg := filepath.Join(victim, "wp-config.php")
	os.WriteFile(victimCfg, []byte(fmt.Sprintf(docrootReadConfig, "victim_db", "victim_user", "10.0.0.9", "vic_")), 0o600)
	os.WriteFile(filepath.Join(victim, ".htaccess"), []byte("Options -Indexes\n"), 0o644)
	os.MkdirAll(filepath.Join(victim, "wp-includes"), 0o755)
	os.WriteFile(filepath.Join(victim, "wp-includes", "version.php"), []byte("<?php $wp_version = '9.9-victim';"), 0o644)

	// 1. Reproduction, twice: symlinked wp-config.php leaks nothing.
	att := t.TempDir()
	os.Symlink(victimCfg, filepath.Join(att, "wp-config.php"))
	for i := 1; i <= 2; i++ {
		_, d := docrootReadCall(att, false)
		_, s := docrootReadCall(att, true)
		all := docrootReadS(d, "db_name") + docrootReadS(d, "db_user") + docrootReadS(d, "db_host") + docrootReadS(s, "table_prefix")
		check(fmt.Sprintf("symlinked wp-config leaks nothing (run %d)", i), !strings.Contains(all, "vic"), all)
		fe, _ := s["file_edit_disabled"].(bool)
		check(fmt.Sprintf("symlinked wp-config hardening not reported (run %d)", i), !fe, "file_edit_disabled=true from victim")
	}

	// 2. Symlinked .htaccess and version.php are not read either.
	att2 := t.TempDir()
	os.WriteFile(filepath.Join(att2, "wp-config.php"), []byte(fmt.Sprintf(docrootReadConfig, "own", "u", "h", "own_")), 0o600)
	os.Symlink(filepath.Join(victim, ".htaccess"), filepath.Join(att2, ".htaccess"))
	os.MkdirAll(filepath.Join(att2, "wp-includes"), 0o755)
	os.Symlink(filepath.Join(victim, "wp-includes", "version.php"), filepath.Join(att2, "wp-includes", "version.php"))
	_, s := docrootReadCall(att2, true)
	dl, _ := s["directory_listing_blocked"].(bool)
	check("symlinked .htaccess not read", !dl, "directory_listing_blocked=true from victim")
	check("symlinked version.php not read", docrootReadS(s, "wp_version") != "9.9-victim", docrootReadS(s, "wp_version"))
	check("own regular wp-config still parsed alongside", docrootReadS(s, "table_prefix") == "own_", docrootReadS(s, "table_prefix"))

	// 3. Dangling symlink: no panic, empty fields.
	att3 := t.TempDir()
	os.Symlink(filepath.Join(victim, "missing"), filepath.Join(att3, "wp-config.php"))
	code, _ := docrootReadCall(att3, true)
	check("dangling wp-config symlink handled", code == http.StatusOK, fmt.Sprintf("code=%d", code))

	// 4. Regular control: unchanged behaviour.
	own := t.TempDir()
	os.WriteFile(filepath.Join(own, "wp-config.php"), []byte(fmt.Sprintf(docrootReadConfig, "own_db", "own_user", "localhost", "own_")), 0o600)
	os.WriteFile(filepath.Join(own, ".htaccess"), []byte("Options -Indexes\n"), 0o644)
	os.MkdirAll(filepath.Join(own, "wp-includes"), 0o755)
	os.WriteFile(filepath.Join(own, "wp-includes", "version.php"), []byte("<?php $wp_version = '6.6.1';"), 0o644)
	_, d := docrootReadCall(own, false)
	_, s = docrootReadCall(own, true)
	check("control db fields", docrootReadS(d, "db_name") == "own_db" && docrootReadS(d, "db_user") == "own_user" && docrootReadS(d, "db_host") == "localhost", fmt.Sprint(d["db_name"], d["db_user"], d["db_host"]))
	check("control version", docrootReadS(d, "version") == "6.6.1" && docrootReadS(s, "wp_version") == "6.6.1", docrootReadS(d, "version"))
	fe, _ := s["file_edit_disabled"].(bool)
	dl, _ = s["directory_listing_blocked"].(bool)
	check("control security flags", fe && dl && docrootReadS(s, "table_prefix") == "own_", fmt.Sprint(s))

	// 5. FIFO: returns promptly, twice.
	for i := 1; i <= 2; i++ {
		root := t.TempDir()
		fifo := filepath.Join(root, "wp-config.php")
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
		done := make(chan struct{})
		go func() { docrootReadCall(root, false); docrootReadCall(root, true); close(done) }()
		select {
		case <-done:
			check(fmt.Sprintf("FIFO wp-config does not block (run %d)", i), true, "")
		case <-time.After(10 * time.Second):
			check(fmt.Sprintf("FIFO wp-config does not block (run %d)", i), false, "blocked >10s")
			for released := false; !released; {
				if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
					w.Close()
				}
				select {
				case <-done:
					released = true
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
	}

	// 6. Directory named wp-config.php: not read, no error escape.
	dirRoot := t.TempDir()
	os.MkdirAll(filepath.Join(dirRoot, "wp-config.php"), 0o755)
	code, _ = docrootReadCall(dirRoot, true)
	check("directory wp-config handled", code == http.StatusOK, fmt.Sprintf("code=%d", code))

	// 7. Size bound: a file over the cap is not read; a large legit file under it is.
	big := t.TempDir()
	os.WriteFile(filepath.Join(big, "wp-config.php"), []byte(fmt.Sprintf(docrootReadConfig, "big_db", "u", "h", "b_")+strings.Repeat("/", 17<<20)), 0o600)
	_, d = docrootReadCall(big, false)
	check("over-cap wp-config not parsed", docrootReadS(d, "db_name") == "", docrootReadS(d, "db_name"))
	under := t.TempDir()
	os.WriteFile(filepath.Join(under, "wp-config.php"), []byte(fmt.Sprintf(docrootReadConfig, "under_db", "u", "h", "u_")+strings.Repeat("/", 15<<20)), 0o600)
	_, d = docrootReadCall(under, false)
	check("15 MiB wp-config still parsed", docrootReadS(d, "db_name") == "under_db", docrootReadS(d, "db_name"))

	// 8. Harden still edits a regular wp-config and refuses a symlinked one.
	hr := t.TempDir()
	os.WriteFile(filepath.Join(hr, "wp-config.php"), []byte("<?php\n/* That's all, stop editing! */\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0o600)
	tr := true
	_, err := wp.Harden(hr, wp.HardenOptions{DisableFileEdit: &tr})
	data, _ := os.ReadFile(filepath.Join(hr, "wp-config.php"))
	check("Harden edits regular wp-config", err == nil && strings.Contains(string(data), "DISALLOW_FILE_EDIT"), fmt.Sprint(err))
	before, _ := os.ReadFile(victimCfg)
	wp.Harden(att, wp.HardenOptions{DisableFileEdit: &tr})
	after, _ := os.ReadFile(victimCfg)
	check("Harden does not touch symlink target", string(before) == string(after), "victim wp-config modified")

	// 9. 16 concurrent requests against the symlinked root, released together.
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	leaks := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(sec bool) {
			defer wg.Done()
			<-start
			_, m := docrootReadCall(att, sec)
			if strings.Contains(docrootReadS(m, "db_name")+docrootReadS(m, "table_prefix"), "vic") {
				mu.Lock()
				leaks++
				mu.Unlock()
			}
		}(i%2 == 0)
	}
	close(start)
	wg.Wait()
	check("16 concurrent requests leak nothing", leaks == 0, fmt.Sprintf("leaks=%d", leaks))

	fmt.Printf("OK=%d MISMATCH=%d\n", ok, bad)
	if bad > 0 {
		t.Fatalf("FIX NOT VERIFIED")
	}
	fmt.Println("FIX VERIFIED")
}
