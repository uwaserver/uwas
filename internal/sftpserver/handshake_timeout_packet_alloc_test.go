package sftpserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"golang.org/x/crypto/ssh"
)

// A client that connects and never completes the SSH handshake must be
// dropped once handshakeTimeout elapses instead of pinning a goroutine and a
// file descriptor forever.
func TestHandshakeTimeoutReapsSilentClient(t *testing.T) {
	old := handshakeTimeout
	handshakeTimeout = 100 * time.Millisecond
	defer func() { handshakeTimeout = old }()

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{}, logger.New("error", "text"))
	s.sshCfg = &ssh.ServerConfig{NoClientAuth: false, PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		return nil, io.EOF
	}}
	s.sshCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	srv, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}

	s.wg.Add(1)
	done := make(chan struct{})
	go func() { s.handleConn(srv); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handleConn still running for a client that never sent its SSH banner")
	}
}

// A packet header declaring a large length must not make the server allocate
// that length before the payload arrives.
func TestReadPacketDoesNotPreallocateDeclaredLength(t *testing.T) {
	const sessions = 8
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	var clis []net.Conn
	var dones []chan struct{}
	for i := 0; i < sessions; i++ {
		srvSide, cli := net.Pipe()
		done := make(chan struct{})
		go func() {
			(&Server{}).serveSFTP(newPipeChannel(srvSide), t.TempDir(), false)
			close(done)
		}()
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], 1<<24)
		if _, err := cli.Write(l[:]); err != nil {
			t.Fatal(err)
		}
		clis = append(clis, cli)
		dones = append(dones, done)
	}
	deadline := time.Now().Add(10 * time.Second)
	for parkedInReadPacket() < sessions {
		if time.Now().After(deadline) {
			t.Fatal("sessions never parked in readPacket")
		}
		runtime.Gosched()
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	for i, c := range clis {
		c.Close()
		<-dones[i]
	}
	if held := int64(after.HeapInuse) - int64(before.HeapInuse); held >= sessions<<20 {
		t.Fatalf("%d stalled sessions hold %d MiB after a 4-byte header each; want < 1 MiB per session", sessions, held>>20)
	}
}

func parkedInReadPacket() int {
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)
	cnt := 0
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "readPacket") && strings.Contains(g, "pipe).read") {
			cnt++
		}
	}
	return cnt
}
