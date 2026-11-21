//go:build unix

package sftpserver

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestFIFORequestsAnswerWithoutWriter: SETSTAT, OPENDIR and OPEN opened a
// tenant-made FIFO with a blocking open(2), which waits for a writer that never
// comes; the session stopped answering and the goroutine stayed pinned to an OS
// thread after the client left (F67401).
func TestFIFORequestsAnswerWithoutWriter(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "p")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	s, addr := startTestServer(t, map[string]User{"user": {Password: "pass", Root: root}})
	defer s.Stop()
	client, sc := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	sc.sendInit()
	if _, _, _, err := sc.recvPacket(); err != nil {
		t.Fatalf("init: %v", err)
	}
	type reply struct {
		typ     byte
		payload []byte
		err     error
	}
	replies := make(chan reply, 4)
	go func() {
		for {
			typ, _, p, err := sc.recvPacket()
			replies <- reply{typ, p, err}
			if err != nil {
				return
			}
		}
	}()

	perm := append(marshalUint32(sshFileXferAttrPermissions), marshalUint32(0o640)...)
	openArgs := func(fl uint32) []byte { return append(marshalUint32(fl), marshalUint32(0)...) }
	cases := []struct {
		name    string
		typ     byte
		payload []byte
		wantOK  bool
	}{
		{"SETSTAT perm", sshFXPSetStat, append(marshalString("p"), perm...), true},
		{"OPENDIR", sshFXPOpenDir, marshalString("p"), false},
		{"OPEN read", sshFXPOpen, append(marshalString("p"), openArgs(sshFXFRead)...), false},
		{"OPEN write", sshFXPOpen, append(marshalString("p"), openArgs(sshFXFWrite)...), false},
	}
	for i, c := range cases {
		sc.sendPacket(c.typ, uint32(i+1), c.payload)
		var r reply
		select {
		case r = <-replies:
		case <-time.After(10 * time.Second): // failure guard only; the fixed code answers at once
			if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				w.Close() // a reader is parked in open(2): release it so the test can end
			}
			t.Fatalf("%s on a FIFO got no answer: open(2) waits for a writer", c.name)
		}
		if r.err != nil {
			t.Fatalf("%s: session broke: %v", c.name, r.err)
		}
		if r.typ != sshFXPStatus || (statusCode(r.payload) == sshFXOK) != c.wantOK {
			t.Fatalf("%s on a FIFO: type=%d code=%d, want ok=%v", c.name, r.typ, statusCode(r.payload), c.wantOK)
		}
	}
	if fi, err := os.Lstat(fifo); err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("SETSTAT on the FIFO did not apply its mode: %v %v", fi.Mode(), err)
	}
}
