package sftpserver

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestServeSFTPClosesHandlesOnDisconnect pins that file handles a client
// opened but never SSH_FXP_CLOSEd are released when the session ends.
// Leaving them open leaked one descriptor per handle for the life of the
// process, so dropped uploads slowly exhausted the server's fd limit.
func TestServeSFTPClosesHandlesOnDisconnect(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counts descriptors via /proc/self/fd")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "upload.bin")
	if err := os.WriteFile(target, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	srvSide, cli := net.Pipe()
	done := make(chan struct{})
	go func() {
		(&Server{}).serveSFTP(newPipeChannel(srvSide), root, false)
		close(done)
	}()

	send := func(typ byte, id uint32, payload []byte) {
		buf := make([]byte, 9+len(payload))
		binary.BigEndian.PutUint32(buf[0:4], uint32(5+len(payload)))
		buf[4] = typ
		binary.BigEndian.PutUint32(buf[5:9], id)
		copy(buf[9:], payload)
		if _, err := cli.Write(buf); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	recv := func() byte {
		var l [4]byte
		if _, err := io.ReadFull(cli, l[:]); err != nil {
			t.Fatalf("read: %v", err)
		}
		b := make([]byte, binary.BigEndian.Uint32(l[:]))
		if _, err := io.ReadFull(cli, b); err != nil {
			t.Fatalf("read: %v", err)
		}
		return b[0]
	}

	send(sshFXPInit, 3, nil) // id field doubles as the version for INIT
	recv()
	open := marshalString("/upload.bin")
	open = append(open, 0, 0, 0, sshFXFRead, 0, 0, 0, 0)
	for i := uint32(1); i <= 3; i++ {
		send(sshFXPOpen, i, open)
		if typ := recv(); typ != sshFXPHandle {
			t.Fatalf("open %d: got packet type %d", i, typ)
		}
	}

	cli.Close() // disconnect without SSH_FXP_CLOSE
	<-done
	srvSide.Close()

	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	leaked := 0
	for _, e := range ents {
		if l, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && l == target {
			leaked++
		}
	}
	if leaked != 0 {
		t.Fatalf("%d descriptors to %s still open after the session ended", leaked, target)
	}
}
