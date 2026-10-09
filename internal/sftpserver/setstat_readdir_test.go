package sftpserver

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// pipeSFTP drives serveSFTP over net.Pipe and returns a request/response func.
func pipeSFTP(t *testing.T, root string, readOnly bool) func(typ byte, payload []byte) (byte, uint32, []byte) {
	t.Helper()
	srvSide, cli := net.Pipe()
	done := make(chan struct{})
	go func() {
		(&Server{}).serveSFTP(newPipeChannel(srvSide), root, readOnly)
		close(done)
	}()
	t.Cleanup(func() { cli.Close(); <-done })
	var id uint32
	rt := func(typ byte, payload []byte) (byte, uint32, []byte) {
		id++
		buf := make([]byte, 9+len(payload))
		binary.BigEndian.PutUint32(buf[0:4], uint32(5+len(payload)))
		buf[4] = typ
		binary.BigEndian.PutUint32(buf[5:9], id)
		copy(buf[9:], payload)
		if _, err := cli.Write(buf); err != nil {
			t.Fatalf("write: %v", err)
		}
		var l [4]byte
		if _, err := io.ReadFull(cli, l[:]); err != nil {
			t.Fatalf("read: %v", err)
		}
		b := make([]byte, binary.BigEndian.Uint32(l[:]))
		if _, err := io.ReadFull(cli, b); err != nil {
			t.Fatalf("read: %v", err)
		}
		if b[0] == sshFXPVersion {
			return b[0], uint32(len(b)), nil
		}
		return b[0], uint32(len(b)), b[5:]
	}
	rt(sshFXPInit, nil)
	return rt
}

func permAttrs(mode uint32) []byte {
	return append(marshalUint32(0x00000004), marshalUint32(mode)...) // SSH_FILEXFER_ATTR_PERMISSIONS
}

// TestSetStatAppliesPermissions pins that SETSTAT really changes the mode,
// strips setuid/setgid/sticky, and is refused in read-only sessions. It used
// to answer OK without doing anything, so a tenant's chmod 600 on a secret
// file silently left it 0644.
func TestSetStatAppliesPermissions(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	p := filepath.Join(root, "secret.txt")
	os.WriteFile(p, []byte("x"), 0644)

	rt := pipeSFTP(t, root, false)
	if _, _, body := rt(sshFXPSetStat, append(marshalString("secret.txt"), permAttrs(0600)...)); statusCode(body) != sshFXOK {
		t.Fatalf("setstat status %d", statusCode(body))
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0600 {
		t.Fatalf("mode %v after chmod 0600", fi.Mode().Perm())
	}
	rt(sshFXPSetStat, append(marshalString("secret.txt"), permAttrs(04755)...))
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0755 || fi.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("mode %v after chmod 04755, want 0755 without setuid", fi.Mode())
	}

	roRoot, _ := filepath.EvalSymlinks(t.TempDir())
	rp := filepath.Join(roRoot, "r.txt")
	os.WriteFile(rp, []byte("x"), 0644)
	ro := pipeSFTP(t, roRoot, true)
	if _, _, body := ro(sshFXPSetStat, append(marshalString("r.txt"), permAttrs(0600)...)); statusCode(body) != sshFXPermissionDenied {
		t.Fatalf("read-only setstat status %d, want permission denied", statusCode(body))
	}
	if fi, _ := os.Stat(rp); fi.Mode().Perm() != 0644 {
		t.Fatalf("read-only session changed mode to %v", fi.Mode().Perm())
	}
}

// TestReadDirBatchesLargeDirectories pins that READDIR pages through a large
// directory. Sending it whole produced NAME packets far over OpenSSH's
// 256 KiB message limit, so the client aborted on big upload/cache folders.
func TestReadDirBatchesLargeDirectories(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	const n = 3000
	for i := 0; i < n; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("%050d.jpg", i)), nil, 0644)
	}
	rt := pipeSFTP(t, root, false)
	_, _, body := rt(sshFXPOpenDir, marshalString("."))
	h, _ := readString(body)
	total := 0
	for {
		typ, length, body := rt(sshFXPReadDir, marshalString(h))
		if length > 256*1024 {
			t.Fatalf("READDIR reply of %d bytes exceeds 256 KiB", length)
		}
		if typ == sshFXPStatus {
			if statusCode(body) != sshFXEOF {
				t.Fatalf("readdir status %d", statusCode(body))
			}
			break
		}
		total += int(binary.BigEndian.Uint32(body[0:4]))
	}
	if total != n {
		t.Fatalf("listed %d entries, want %d", total, n)
	}
}
