package server

// Regression tests for F691/F700: the server and the admin API share one
// config lock; reload holds it from config.Load through the in-place
// overwrite, so an admin write cannot land in between and be wiped; and the
// onDomainChange callback copies the domains instead of walking the live
// slice. The reload config path is a FIFO so reload parks inside config.Load
// while holding the lock; goroutine stacks confirm each party is parked, and
// every wait is bounded so a deadlock fails instead of hanging.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

var sharedLockT *testing.T

func sharedLockCheck(name string, ok bool, detail string) {
	if !ok {
		sharedLockT.Errorf("%s: %s", name, detail)
	}
}

func sharedLockStacks() string {
	buf := make([]byte, 8<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

func sharedLockParked(t *testing.T, what string, pred func(g string) bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		for _, g := range strings.Split(sharedLockStacks(), "\n\n") {
			if pred(g) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("never observed %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func sharedLockWait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(30 * time.Second):
		t.Fatalf("DEADLOCK? %s did not finish within 30s\n%s", what, sharedLockStacks())
	}
	var zero T
	return zero
}

const sharedLockYAML = "global:\n  worker_count: \"1\"\n  log_level: error\n  log_format: text\n  web_root: %s\n  admin:\n    enabled: %v\n    listen: \"%s\"\ndomains:\n  - host: a.test\n    type: static\n    root: %s\n    ssl:\n      mode: \"off\"\n"

func sharedLockSetup(t *testing.T, adminOn bool) (s *Server, base, cfgPath, yaml string) {
	dir := t.TempDir()
	root := filepath.Join(dir, "www")
	os.MkdirAll(root, 0o755)
	cfgPath = filepath.Join(dir, "uwas.yaml")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	yaml = fmt.Sprintf(sharedLockYAML, root, adminOn, addr, root)
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	s = New(cfg, logger.New("error", "text"))
	s.SetConfigPath(cfgPath)
	if !adminOn {
		return s, "", cfgPath, yaml
	}
	go s.admin.Start()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admin not up")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		if srv := s.admin.HTTPServer(); srv != nil {
			srv.Close()
		}
	})
	return s, "http://" + addr, cfgPath, yaml
}

func sharedLockAdd(base, host, root string) int {
	body, _ := json.Marshal(map[string]any{"host": host, "type": "static", "root": root, "ssl": map[string]any{"mode": "off"}})
	resp, err := http.Post(base+"/api/v1/domains", "application/json", bytes.NewReader(body))
	if err != nil {
		return -1
	}
	resp.Body.Close()
	return resp.StatusCode
}

func sharedLockHas(s *Server, host string) bool {
	s.cfgMu().RLock()
	defer s.cfgMu().RUnlock()
	for _, d := range s.config.Domains {
		if d.Host == host {
			return true
		}
	}
	return false
}

// Scenario A: an admin add that arrives while reload is inside config.Load
// waits for the reload and survives it.
func sharedLockAddDuringReload(t *testing.T, round int) {
	s, base, cfgPath, yaml := sharedLockSetup(t, true)
	root := s.config.Global.WebRoot
	fifo := filepath.Join(filepath.Dir(cfgPath), "reload.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	s.configPath = fifo // admin keeps persisting to cfgPath

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- s.reload() }()
	sharedLockParked(t, "reload parked in config.Load holding the lock", func(g string) bool {
		return strings.Contains(g, "(*Server).reload(") && strings.Contains(g, "config.Load(")
	})
	addDone := make(chan int, 1)
	go func() { addDone <- sharedLockAdd(base, "b.test", root) }()
	// The admin request blocks on the shared lock (first in the auth
	// middleware's RLock, later in Add's write lock).
	sharedLockParked(t, "admin add request parked on the shared lock", func(g string) bool {
		return strings.Contains(g, "uwas/internal/admin") && strings.Contains(g, "sync.(*RWMutex).")
	})
	if err := os.WriteFile(fifo, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	rerr := sharedLockWait(t, reloadDone, "reload")
	code := sharedLockWait(t, addDone, "admin add")
	has := sharedLockHas(s, "b.test")
	sharedLockCheck(fmt.Sprintf("A%d add during reload: reload ok", round), rerr == nil, fmt.Sprint(rerr))
	sharedLockCheck(fmt.Sprintf("A%d add during reload: 201 and present", round), code == http.StatusCreated && has,
		fmt.Sprintf("code=%d present=%v", code, has))
	disk, err := config.Load(cfgPath)
	onDisk := false
	if err == nil {
		for _, d := range disk.Domains {
			onDisk = onDisk || d.Host == "b.test"
		}
	}
	sharedLockCheck(fmt.Sprintf("A%d persisted config loads and has b.test", round), err == nil && onDisk, fmt.Sprintf("err=%v onDisk=%v", err, onDisk))
}

// Scenario B: a reload that arrives while an admin writer holds the lock
// waits for it and then applies the file the writer persisted.
func sharedLockReloadDuringAdminWrite(t *testing.T) {
	s, _, cfgPath, yaml := sharedLockSetup(t, true)
	adminMu, _ := s.admin.ConfigLocks()
	sharedLockCheck("B server and admin use one lock", s.cfgMu() == adminMu, "different mutexes")

	adminMu.Lock() // an admin writer's critical section
	reloadDone := make(chan error, 1)
	go func() { reloadDone <- s.reload() }()
	sharedLockParked(t, "reload parked on the shared lock", func(g string) bool {
		return strings.Contains(g, "(*Server).reload(") && strings.Contains(g, "sync.(*RWMutex).Lock")
	})
	// The writer changes the file the reload will read, then releases.
	withC := strings.Replace(yaml, "domains:\n", "domains:\n  - host: c.test\n    type: static\n    root: "+s.config.Global.WebRoot+"\n    ssl:\n      mode: \"off\"\n", 1)
	if err := os.WriteFile(cfgPath, []byte(withC), 0o600); err != nil {
		t.Fatal(err)
	}
	adminMu.Unlock()
	rerr := sharedLockWait(t, reloadDone, "reload")
	sharedLockCheck("B reload after writer: ok and sees c.test", rerr == nil && sharedLockHas(s, "c.test"), fmt.Sprintf("err=%v", rerr))
}

// Scenario C: a failed reload releases both locks.
func sharedLockFailedReloadReleases(t *testing.T) {
	s, base, cfgPath, _ := sharedLockSetup(t, true)
	os.WriteFile(cfgPath, []byte("global: [\n"), 0o600)
	rerr := s.reload()
	code := sharedLockWait(t, func() <-chan int {
		ch := make(chan int, 1)
		go func() { ch <- sharedLockAdd(base, "d.test", s.config.Global.WebRoot) }()
		return ch
	}(), "admin add after failed reload")
	sharedLockCheck("C failed reload returns error, admin add then succeeds", rerr != nil && code == http.StatusCreated,
		fmt.Sprintf("err=%v code=%d", rerr, code))
}

// Scenario D: concurrent admin adds, reloads and request-path readers,
// released together; no deadlock, no data race (-race), config still loads.
func sharedLockBurst(t *testing.T) {
	s, base, cfgPath, _ := sharedLockSetup(t, true)
	root := s.config.Global.WebRoot
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes <- sharedLockAdd(base, fmt.Sprintf("burst%d.test", i), root)
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _ = s.reload() }()
	}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _ = s.graceTTL(); _ = s.altSvcHeader() }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(start)
	sharedLockWait(t, done, "burst")
	close(codes)
	bad := 0
	for c := range codes {
		if c != http.StatusCreated {
			bad++
		}
	}
	_, err := config.Load(cfgPath)
	sharedLockCheck("D burst: all adds 201, no deadlock, config loads", bad == 0 && err == nil, fmt.Sprintf("bad=%d err=%v", bad, err))
}

// Scenario E: without the admin API the server uses its own lock.
func sharedLockNoAdmin(t *testing.T) {
	s, _, _, _ := sharedLockSetup(t, false)
	err := s.reload()
	sharedLockCheck("E no admin: own lock, reload ok", s.cfgMu() == &s.configMu && err == nil, fmt.Sprint(err))
}

func TestReloadAndAdminShareConfigLock(t *testing.T) {
	sharedLockT = t
	sharedLockAddDuringReload(t, 1)
	sharedLockAddDuringReload(t, 2)
	sharedLockReloadDuringAdminWrite(t)
	sharedLockFailedReloadReleases(t)
	sharedLockBurst(t)
	sharedLockNoAdmin(t)
}
