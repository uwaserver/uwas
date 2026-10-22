package sftpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPosixMode(t *testing.T) {
	for _, c := range []struct {
		name string
		in   os.FileMode
		want uint32
	}{
		{"regular", 0o644, 0o100644},
		{"empty perm regular", 0, 0o100000},
		{"dir", os.ModeDir | 0o755, 0o040755},
		{"symlink", os.ModeSymlink | 0o777, 0o120777},
		{"fifo", os.ModeNamedPipe | 0o600, 0o010600},
		{"socket", os.ModeSocket | 0o600, 0o140600},
		{"char dev", os.ModeDevice | os.ModeCharDevice | 0o600, 0o020600},
		{"block dev", os.ModeDevice | 0o600, 0o060600},
		{"setuid file", os.ModeSetuid | 0o755, 0o104755},
		{"setgid dir", os.ModeDir | os.ModeSetgid | 0o755, 0o042755},
		{"sticky dir", os.ModeDir | os.ModeSticky | 0o777, 0o041777},
	} {
		if got := posixMode(c.in); got != c.want {
			t.Errorf("%s: posixMode(%v) = %o, want %o", c.name, c.in, got, c.want)
		}
	}
}

// A symlink listed by READDIR is lstat'ed, so it must read as S_IFLNK.
func TestEncodeAttrsSymlinkAndDirOnTheWire(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "d"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("d", filepath.Join(tmp, "l")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	di, _ := os.Lstat(filepath.Join(tmp, "d"))
	li, _ := os.Lstat(filepath.Join(tmp, "l"))
	if got := permOf(t, di); got != 0o040750 {
		t.Errorf("dir on the wire = %o, want 040750", got)
	}
	if got := permOf(t, li); got&0o170000 != 0o120000 {
		t.Errorf("symlink on the wire = %o, want S_IFLNK", got)
	}
}
