package sftpserver

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// shutdownWaiting reports whether a goroutine is inside Server.Shutdown's
// WaitGroup.Wait, i.e. past the point where it closes live connections.
func shutdownWaiting() bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "sftpserver.(*Server).Shutdown") && strings.Contains(g, "sync.(*WaitGroup).Wait") {
			return true
		}
	}
	return false
}

// TestShutdownClosesIdleSessions: Shutdown, the last step of UWAS's own stop,
// waited for every authenticated client to disconnect by itself, so one idle
// SFTP session held the process's stop open indefinitely (F67400).
func TestShutdownClosesIdleSessions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0644)
	s, addr := startTestServer(t, map[string]User{"user": {Password: "pass", Root: root}})
	client, sc := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	sc.sendInit()
	if _, _, _, err := sc.recvPacket(); err != nil {
		t.Fatalf("init: %v", err)
	}

	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second): // failure guard only
		t.Fatal("Shutdown is still waiting for an idle client to disconnect")
	}
	err := sc.sendPacket(sshFXPStat, 1, marshalString("f.txt"))
	if err == nil {
		_, _, _, err = sc.recvPacket()
	}
	if err == nil {
		t.Fatal("session still served after Shutdown returned")
	}
}

// TestShutdownRefusesHandshakeFinishingLate: a connection whose login completes
// after Shutdown closed the live ones is not served (it would hold Shutdown's
// Wait open the same way).
func TestShutdownRefusesHandshakeFinishingLate(t *testing.T) {
	park := make(chan struct{})
	parked := make(chan struct{})
	prev := acceptSeam
	acceptSeam = func(net.Conn) { close(parked); <-park }
	s, addr := startTestServer(t, map[string]User{"user": {Password: "pass", Root: t.TempDir()}})

	type dialResult struct {
		c   *ssh.Client
		err error
	}
	dialed := make(chan dialResult, 1)
	go func() {
		c, err := ssh.Dial("tcp", addr, sshClientConfig("user", "pass"))
		dialed <- dialResult{c, err}
	}()
	<-parked
	acceptSeam = prev

	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	for !shutdownWaiting() {
		runtime.Gosched()
	}
	close(park)

	if r := <-dialed; r.err == nil {
		ch, _, err := r.c.OpenChannel("session", nil)
		if err == nil {
			ch.Close()
		}
		r.c.Close()
		if err == nil {
			t.Fatal("a connection that logged in after Shutdown was served a session")
		}
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second): // failure guard only
		t.Fatal("Shutdown did not return")
	}
}
