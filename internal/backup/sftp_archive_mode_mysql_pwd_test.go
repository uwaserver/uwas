package backup

// Regression tests for F705 (SFTP archives must be owner-only regardless of the
// remote login umask) and F706 (the mysql password must not appear on argv).

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/uwaserver/uwas/internal/logger"
)

// startShellExecSSHServer serves exec requests by running them through
// /bin/sh -c with a 022 umask, wiring stdin/stdout like sshd does.
func startShellExecSSHServer(t *testing.T, password string) (string, int) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						nc.Reject(ssh.UnknownChannelType, "")
						continue
					}
					ch, chReqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for req := range chReqs {
							if req.Type != "exec" {
								req.Reply(false, nil)
								continue
							}
							n := binary.BigEndian.Uint32(req.Payload[:4])
							cmdStr := string(req.Payload[4 : 4+n])
							req.Reply(true, nil)
							cmd := exec.Command("/bin/sh", "-c", "umask 022\n"+cmdStr)
							cmd.Stdin = ch
							cmd.Stdout = ch
							cmd.Stderr = ch.Stderr()
							status := uint32(0)
							if err := cmd.Run(); err != nil {
								status = 1
								if ee, ok := err.(*exec.ExitError); ok {
									status = uint32(ee.ExitCode())
								}
							}
							var b [4]byte
							binary.BigEndian.PutUint32(b[:], status)
							ch.SendRequest("exit-status", false, b[:])
							return
						}
					}()
				}
			}(c)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func TestSFTPUploadArchiveIsOwnerOnly(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	host, port := startShellExecSSHServer(t, "pw")
	remote := filepath.Join(t.TempDir(), "remote")
	sp := NewSFTPProvider(host, port, "u", "", "pw", remote, true)
	for i := 0; i < 2; i++ {
		if err := sp.Upload(context.Background(), "uwas-backup-r.tar.gz", bytes.NewReader([]byte("SECRET"))); err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
		fi, err := os.Stat(filepath.Join(remote, "uwas-backup-r.tar.gz"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("upload %d: archive mode %04o under remote umask 022, want owner-only", i, fi.Mode().Perm())
		}
	}
}

func TestDatabaseImportPasswordNotOnArgv(t *testing.T) {
	bin := t.TempDir()
	rec := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + rec + "/argv\nprintf '%s' \"${MYSQL_PWD-}\" > " + rec + "/pwd\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(bin, "mysql"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("UWAS_DB_USER", "root")
	t.Setenv("UWAS_DB_PASSWORD", "s3cr3t-pw")
	if err := importDatabaseDumpReal([]byte("SELECT 1;"), logger.New("error", "text")); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(filepath.Join(rec, "argv"))
	pwd, _ := os.ReadFile(filepath.Join(rec, "pwd"))
	if strings.Contains(string(argv), "s3cr3t-pw") {
		t.Fatalf("password on mysql argv: %q", argv)
	}
	if string(pwd) != "s3cr3t-pw" {
		t.Fatalf("mysql did not receive the password via MYSQL_PWD: %q", pwd)
	}
	if !strings.Contains(string(argv), "--user=root") {
		t.Fatalf("connect args lost: %q", argv)
	}
}
