package backup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startStallingSSHServer accepts mkdir/rm/mv (exit 0) but never reads stdin of
// the upload `cat`, nor ends it, until release is closed. It models a remote
// whose network went silent after the SSH handshake.
func startStallingSSHServer(t *testing.T, release <-chan struct{}) (string, int) {
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
							cmd := string(r.Payload[4:])
							if strings.Contains(cmd, "cat >") || strings.Contains(cmd, "cat --") || strings.Contains(cmd, "find ") {
								<-release
							}
							ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
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

// A transfer that stalls after the SSH session is established must be
// abandoned when its context is cancelled (the idle-timeout mechanism cancels
// the context); previously only the TCP dial honoured it.
func TestSFTPOperationsAbandonedOnContextCancel(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	host, port := startStallingSSHServer(t, release)
	p := NewSFTPProvider(host, port, "u", "", "p", "/b", true)

	cases := map[string]func(ctx context.Context) error{
		"upload": func(ctx context.Context) error {
			return p.Upload(ctx, "a.tar.gz", bytes.NewReader(make([]byte, 16<<20)))
		},
		"list": func(ctx context.Context) error { _, err := p.List(ctx); return err },
		"download": func(ctx context.Context) error {
			rc, err := p.Download(ctx, "a.tar.gz")
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = io.Copy(io.Discard, rc)
			return err
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- fn(ctx) }()
			// Gate: cancel only once the operation is in flight (it cannot
			// finish on its own — the server holds the command open).
			select {
			case err := <-done:
				t.Fatalf("finished before cancel: %v", err)
			case <-time.After(300 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("EXPECTED: operation abandoned after ctx cancel; ACTUAL: still blocked")
			}
		})
	}
}
