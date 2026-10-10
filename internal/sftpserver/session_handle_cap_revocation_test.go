package sftpserver

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestSessionHandleCap(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644)
	rt := pipeSFTP(t, root, false)
	open := func() (byte, string) {
		p := append(marshalString("f"), marshalUint32(sshFXFRead)...)
		p = append(p, marshalUint32(0)...)
		typ, _, pl := rt(sshFXPOpen, p)
		if typ == sshFXPHandle {
			h, _ := readString(pl)
			return typ, h
		}
		return typ, ""
	}
	var handles []string
	for i := 0; i < maxSessionHandles; i++ { // boundary: exactly the cap is allowed
		typ, h := open()
		if typ != sshFXPHandle {
			t.Fatalf("open %d refused below cap", i)
		}
		handles = append(handles, h)
	}
	if typ, _ := open(); typ == sshFXPHandle {
		t.Fatal("open beyond cap granted")
	}
	// directories count against the same cap
	if typ, _, _ := rt(sshFXPOpenDir, marshalString(".")); typ == sshFXPHandle {
		t.Fatal("opendir beyond cap granted")
	}
	// closing frees a slot
	rt(sshFXPClose, marshalString(handles[0]))
	if typ, _ := open(); typ != sshFXPHandle {
		t.Fatal("open after close refused")
	}
	if typ, _ := open(); typ == sshFXPHandle {
		t.Fatal("cap not re-enforced after refill")
	}
}

func TestSessionEndsWhenUserChanges(t *testing.T) {
	hash := func(p string) string {
		h, _ := bcrypt.GenerateFromPassword([]byte(p), bcrypt.MinCost)
		return string(h)
	}
	root := t.TempDir()
	base := User{Password: hash("pass"), Root: root}
	cases := []struct {
		name   string
		next   map[string]User
		wantOK bool
	}{
		{"unchanged", map[string]User{"user": base}, true},
		{"removed", map[string]User{}, false},
		{"other-user-only", map[string]User{"x": base}, false},
		{"password-changed", map[string]User{"user": {Password: hash("new"), Root: root}}, false},
		{"root-changed", map[string]User{"user": {Password: base.Password, Root: t.TempDir()}}, false},
		{"made-read-only", map[string]User{"user": {Password: base.Password, Root: root, ReadOnly: true}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, addr := startTestServer(t, map[string]User{"user": base})
			defer s.Stop()
			client, sftp := openSFTPSession(t, addr, "user", "pass")
			defer client.Close()
			defer sftp.ch.Close()
			sftp.sendInit()
			sftp.recvPacket()
			stat := func(id uint32) bool {
				sftp.sendPacket(sshFXPStat, id, marshalString("."))
				typ, _, _, err := sftp.recvPacket()
				return err == nil && typ == sshFXPAttrs
			}
			if !stat(1) {
				t.Fatal("control stat failed")
			}
			s.UpdateUsers(c.next)
			if got := stat(2); got != c.wantOK {
				t.Fatalf("stat after update served=%v want %v", got, c.wantOK)
			}
			if got := stat(3); got != c.wantOK { // repeated call stays consistent
				t.Fatalf("repeat stat served=%v want %v", got, c.wantOK)
			}
		})
	}
}
