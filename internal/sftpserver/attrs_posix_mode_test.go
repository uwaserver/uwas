package sftpserver

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// permOf returns the permissions word encodeAttrs puts on the wire.
func permOf(t *testing.T, info os.FileInfo) uint32 {
	t.Helper()
	return binary.BigEndian.Uint32(encodeAttrs(info)[20:24])
}

// F24700: SFTP v3 permissions are a POSIX st_mode. encodeAttrs used to send Go's
// os.FileMode, whose directory bit is 1<<31, so S_ISDIR(perm) was false for
// every directory (OpenSSH's sftp refuses "cd" into it).
func TestEncodeAttrsPOSIXTypeBits(t *testing.T) {
	const sIFMT, sIFDIR, sIFREG = 0o170000, 0o040000, 0o100000
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dinfo, _ := os.Stat(filepath.Join(tmp, "d"))
	finfo, _ := os.Stat(filepath.Join(tmp, "f"))

	// Unaffected control: the permission bits survive for both.
	if permOf(t, dinfo)&0o777 != 0o755 || permOf(t, finfo)&0o777 != 0o644 {
		t.Fatalf("control failed: dir %o file %o", permOf(t, dinfo)&0o777, permOf(t, finfo)&0o777)
	}
	dir, file := permOf(t, dinfo), permOf(t, finfo)
	t.Logf("EXPECTED: dir type bits %o, file type bits %o", sIFDIR, sIFREG)
	t.Logf("ACTUAL:   dir type bits %o (perm %#x), file type bits %o", dir&sIFMT, dir, file&sIFMT)
	if dir&sIFMT != sIFDIR || file&sIFMT != sIFREG {
		t.Fatal("PROBLEM CONFIRMED")
	}
	t.Log("PROBLEM NOT REPRODUCED")
}
