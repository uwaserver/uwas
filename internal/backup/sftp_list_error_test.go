package backup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// startListSSHServer answers every exec with onExec: exit is the exit status
// to send (-1 closes the channel without one, as a dropped connection does)
// and out is written to stdout first.
func startListSSHServer(t *testing.T, exit int, out string) (string, int) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "u" && string(p) == "p" {
			return nil, nil
		}
		return nil, fmt.Errorf("auth failed")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, rq, err := nc.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for r := range rq {
							if r.Type != "exec" {
								if r.WantReply {
									r.Reply(false, nil)
								}
								continue
							}
							if r.WantReply {
								r.Reply(true, nil)
							}
							if strings.Contains(string(r.Payload[4:]), "find ") {
								ch.Write([]byte(out))
								if exit >= 0 {
									ch.SendRequest("exit-status", false, []byte{0, 0, 0, byte(exit)})
								}
							}
							return
						}
					}()
				}
			}()
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// F58001: List turned every failure of the listing command into "no backups",
// a dropped connection included, so the dashboard showed an empty remote and
// the error never reached the log. Only the remote reporting a failed command
// (missing directory) means "nothing there".
func TestSFTPListReportsTransportFailure(t *testing.T) {
	t.Run("connection dropped mid-listing", func(t *testing.T) {
		host, port := startListSSHServer(t, -1, "a.tar.gz\t10\t1700000000.0\n")
		p := NewSFTPProvider(host, port, "u", "", "p", "/b", true)
		if _, err := p.List(context.Background()); err == nil {
			t.Fatal("EXPECTED: error for a listing that ended without an exit status; ACTUAL: nil")
		}
	})
	t.Run("missing directory is empty", func(t *testing.T) {
		host, port := startListSSHServer(t, 2, "")
		p := NewSFTPProvider(host, port, "u", "", "p", "/b", true)
		infos, err := p.List(context.Background())
		if err != nil || len(infos) != 0 {
			t.Fatalf("missing remote dir: infos=%v err=%v, want empty and nil", infos, err)
		}
	})
	t.Run("complete listing", func(t *testing.T) {
		host, port := startListSSHServer(t, 0, "a.tar.gz\t10\t1700000000.0\n")
		p := NewSFTPProvider(host, port, "u", "", "p", "/b", true)
		infos, err := p.List(context.Background())
		if err != nil || len(infos) != 1 || infos[0].Name != "a.tar.gz" {
			t.Fatalf("listing: infos=%v err=%v", infos, err)
		}
	})
}
