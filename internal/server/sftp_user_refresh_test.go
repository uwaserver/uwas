package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestSFTPUsersFollowDomainDeleteAndKeyRotation pins F715: the SFTP user
// table was built once in Start() and never refreshed, so a domain deleted
// through the admin API kept its SFTP login, and passwords derived from a
// rotated api_key stayed valid until restart.
func TestSFTPUsersFollowDomainDeleteAndKeyRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("bcrypt at auth.BcryptCost is slow")
	}
	freeAddr := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().String()
	}
	waitTCP := func(addr string) {
		deadline := time.Now().Add(180 * time.Second)
		for {
			c, err := net.Dial("tcp", addr)
			if err == nil {
				c.Close()
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never became reachable: %v", addr, err)
			}
			time.Sleep(5 * time.Millisecond) // readiness wait only
		}
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	sftpAddr, adminAddr := freeAddr(), freeAddr()
	oldKey := "rt715-old-key-0123456789abcdefABCDEF"
	var b strings.Builder
	fmt.Fprintf(&b, "global:\n  log_level: error\n  log_format: text\n  http_listen: %s\n  sftp_listen: %s\n  web_root: %s\n  admin:\n    enabled: true\n    listen: %s\n    api_key: %s\ndomains:\n",
		freeAddr(), sftpAddr, dir, adminAddr, oldKey)
	for _, h := range []string{"a.test", "b.test"} {
		root := filepath.Join(dir, h, "public_html")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "  - host: %s\n    type: static\n    root: %s\n    ssl:\n      mode: \"off\"\n", h, root)
	}
	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	s := New(cfg, logger.New("error", "text"))
	s.SetConfigPath(cfgPath)
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	t.Cleanup(func() {
		s.cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Errorf("Start did not return after cancel")
		}
	})
	waitTCP(sftpAddr)
	waitTCP(adminAddr)

	req, _ := http.NewRequest(http.MethodDelete, "http://"+adminAddr+"/api/v1/domains/a.test?confirm=true", nil)
	req.Header.Set("Authorization", "Bearer "+oldKey)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE a.test = %d", resp.StatusCode)
	}

	client := &ssh.ClientConfig{
		User:            "a.test",
		Auth:            []ssh.AuthMethod{ssh.Password(deriveSFTPPassword(oldKey, "a.test"))},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         60 * time.Second,
	}
	if c, err := ssh.Dial("tcp", sftpAddr, client); err == nil {
		c.Close()
		t.Fatal("deleted domain a.test can still log in over SFTP")
	}

	newKey := "rt715-new-key-ZYXWVUTSRQponmlk98765"
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(strings.ReplaceAll(string(data), oldKey, newKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	s.sftpMu.Lock()
	entry, ok := s.sftpHashes["b.test"]
	_, stale := s.sftpHashes["a.test"]
	s.sftpMu.Unlock()
	if stale {
		t.Fatal("deleted domain a.test still in the SFTP user table")
	}
	if !ok {
		t.Fatal("b.test missing from the SFTP user table after reload")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(entry.hash), []byte(deriveSFTPPassword(newKey, "b.test"))); err != nil {
		t.Fatalf("b.test SFTP password not rotated to the new api_key: %v", err)
	}
}
