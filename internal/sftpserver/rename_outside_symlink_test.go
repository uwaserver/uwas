package sftpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// Renaming a symlink that points outside the chroot moves only the link, like
// removing it does (F25200); paths THROUGH the link and traversal stay denied.
func TestSFTP_RenameSymlinkPointingOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "outlink")); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, addr := startTestServer(t, map[string]User{"user": {Password: "pass", Root: root}})
	defer s.Stop()
	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()
	sftp.sendInit()
	sftp.recvPacket()

	rename := func(id uint32, from, to string) uint32 {
		sftp.sendPacket(sshFXPRename, id, append(marshalString(from), marshalString(to)...))
		pt, _, payload, err := sftp.recvPacket()
		if err != nil || pt != sshFXPStatus {
			t.Fatalf("rename %q: type=%d err=%v", from, pt, err)
		}
		return statusCode(payload)
	}

	// control: a plain file renames
	if got := rename(1, "plain.txt", "plain2.txt"); got != sshFXOK {
		t.Fatalf("control rename: status %d", got)
	}
	if got := rename(2, "outlink", "outlink2"); got != sshFXOK {
		t.Errorf("rename outlink: status %d, want OK", got)
	}
	if got := rename(3, "escape", "escape2"); got != sshFXOK {
		t.Errorf("rename escape link: status %d, want OK", got)
	}
	if _, err := os.Lstat(filepath.Join(root, "outlink2")); err != nil {
		t.Errorf("outlink2 missing: %v", err)
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "keep" {
		t.Fatalf("outside target touched: %v %q", err, b)
	}
	for i, p := range [][2]string{
		{"escape2/victim.txt", "x"},
		{"plain2.txt", "escape2/y"},
		{"../victim.txt", "x"},
		{"plain2.txt", "../z"},
	} {
		if got := rename(uint32(10+i), p[0], p[1]); got == sshFXOK {
			t.Errorf("rename %q -> %q was allowed", p[0], p[1])
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "y")); err == nil {
		t.Error("file escaped via link")
	}
}
