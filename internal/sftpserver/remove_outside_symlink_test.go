package sftpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink in the chroot that points outside it (planted by a tenant's PHP
// symlink()) must be removable over SFTP — removal touches only the link —
// while the target stays intact and paths THROUGH the link stay denied (F1601).
func TestSFTP_RemoveSymlinkPointingOutside(t *testing.T) {
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

	remove := func(id uint32, path string) uint32 {
		sftp.sendPacket(sshFXPRemove, id, marshalString(path))
		pt, _, payload, err := sftp.recvPacket()
		if err != nil || pt != sshFXPStatus {
			t.Fatalf("remove %q: type=%d err=%v", path, pt, err)
		}
		return statusCode(payload)
	}

	if got := remove(1, "outlink"); got != sshFXOK {
		t.Fatalf("remove outlink: status %d, want OK", got)
	}
	if _, err := os.Lstat(filepath.Join(root, "outlink")); err == nil {
		t.Error("outlink still present")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("outside target touched: %v", err)
	}
	// through the escaping link, traversal, the root and a regular outside
	// file remain denied; a plain file still works.
	for i, p := range []string{"escape/victim.txt", "../victim.txt", "/", "."} {
		if got := remove(uint32(10+i), p); got == sshFXOK {
			t.Errorf("remove %q was allowed", p)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim removed via escape link: %v", err)
	}
	if got := remove(20, "plain.txt"); got != sshFXOK {
		t.Errorf("remove plain.txt: status %d", got)
	}
	// the escaping directory link itself can be unlinked too
	if got := remove(21, "escape"); got != sshFXOK {
		t.Errorf("remove escape link: status %d", got)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim lost after unlinking directory link: %v", err)
	}
}
