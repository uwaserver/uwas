package siteuser

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestCreateUserChrootDirIsOneSSHDArgument pins F356: the domain dir is the
// sshd ChrootDirectory argument. A space must be quoted (unquoted, sshd sees
// extra arguments and rejects the config, leaving the account unchrooted), and
// a newline must be refused before any account exists.
func TestCreateUserChrootDirIsOneSSHDArgument(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	_, sshd := siteuserTempSetup(t, "Subsystem sftp internal-sftp\n")
	domainDir := filepath.Join(t.TempDir(), "My Site")
	if _, _, err := CreateUserForWebDir(filepath.Join(domainDir, "public_html"), "a.com"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(sshd)
	if !strings.Contains(string(data), `ChrootDirectory "`+domainDir+`"`) {
		t.Fatalf("space in chroot dir not quoted:\n%s", data)
	}

	_, sshd = siteuserTempSetup(t, "Subsystem sftp internal-sftp\n")
	var calls []string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name)
		return fakeOutputCmd(0)
	}
	bad := filepath.Join(t.TempDir(), "x\nPasswordAuthentication yes\n#", "public_html")
	if _, _, err := CreateUserForWebDir(bad, "b.com"); err == nil {
		t.Fatal("newline in chroot dir accepted")
	}
	if len(calls) != 0 {
		t.Fatalf("commands ran before rejection: %v", calls)
	}
	data, _ = os.ReadFile(sshd)
	if strings.Contains(string(data), "PasswordAuthentication") {
		t.Fatalf("directive injected into sshd_config:\n%s", data)
	}
}

// TestDeleteUserRevokesAuthorizedKeys pins F357: deleting the SFTP user must
// also drop its authorized_keys, or re-creating the user for the domain
// re-authorizes every key the old account had.
func TestDeleteUserRevokesAuthorizedKeys(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	tmp, _ := siteuserTempSetup(t, "Subsystem sftp internal-sftp\n")
	webDir := filepath.Join(tmp, "a.com", "public_html")
	passwd := filepath.Join(tmp, "passwd")
	if err := os.WriteFile(passwd, []byte("uwas-a--com:x:1001:33::"+filepath.Dir(webDir)+":/usr/sbin/nologin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	passwdPath = passwd
	if _, _, err := CreateUserForWebDir(webDir, "a.com"); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddSSHKeyForWebDir(webDir, "a.com", string(ssh.MarshalAuthorizedKey(sp))); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUser("a.com"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CreateUserForWebDir(webDir, "a.com"); err != nil {
		t.Fatal(err)
	}
	if keys := ListSSHKeysForWebDir(webDir, "a.com"); len(keys) != 0 {
		t.Fatalf("key from the deleted account still authorized: %v", keys)
	}
}
