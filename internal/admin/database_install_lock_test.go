package admin

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/uwaserver/uwas/internal/install"
)

func parkedIn(fn string) bool {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		if strings.Contains(g, fn) && strings.Contains(header, "[sync.") {
			return true
		}
	}
	return false
}

// TestDatabaseInstallSerializedWithPackageInstall: a database install must not
// queue MariaDB while a package install has passed its "another installation
// in progress" check but not yet submitted.
func TestDatabaseInstallSerializedWithPackageInstall(t *testing.T) {
	// PATH holds only a fake apt that parks on a FIFO and then fails, so
	// queued installs stay active and nothing real can run.
	bin := t.TempDir()
	gate := filepath.Join(bin, "gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "apt"), []byte("#!/bin/sh\nread x < '"+gate+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	restore := setSystemExecCommand(func(string, ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "read x < '"+gate+"'")
	})
	defer restore()

	s := testServer()
	q := install.New()
	s.taskMgr = q
	defer q.Stop()
	defer func() {
		for q.Active() != nil {
			if f, err := os.OpenFile(gate, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				f.Write([]byte("x\n"))
				f.Close()
			}
			runtime.Gosched()
		}
	}()

	post := func(path, body string) int {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("POST", path, strings.NewReader(body))))
		return rec.Code
	}

	// Park the package install between its Active() check and Submit() by
	// holding the audit lock recordAuditR takes in between.
	s.auditMu.Lock()
	pkgDone := make(chan int, 1)
	go func() { pkgDone <- post("/api/v1/packages/install", `{"id":"mariadb"}`) }()
	for !parkedIn("handlePackageInstall") {
		runtime.Gosched()
	}

	dbDone := make(chan int, 1)
	go func() { dbDone <- post("/api/v1/database/install", "") }()
	dbCode := 0
	for dbCode == 0 && !parkedIn("handleDBInstall") {
		select {
		case dbCode = <-dbDone:
		default:
			runtime.Gosched()
		}
	}
	s.auditMu.Unlock()
	pkgCode := <-pkgDone
	if dbCode == 0 {
		dbCode = <-dbDone
	}

	dbTasks := 0
	for _, tk := range q.List() {
		if tk.Type == "database" {
			dbTasks++
		}
	}
	if pkgCode != 200 || dbCode != 409 || dbTasks != 0 {
		t.Fatalf("package=%d database=%d databaseTasks=%d; want 200, 409, 0", pkgCode, dbCode, dbTasks)
	}
}
