package siteuser

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// chrootFor returns the ChrootDirectory of the Match block for exactly username.
func chrootFor(content, username string) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "Match User "+username {
			continue
		}
		for _, n := range lines[i+1:] {
			tn := strings.TrimSpace(n)
			if strings.HasPrefix(tn, "Match ") {
				break
			}
			if strings.HasPrefix(tn, "ChrootDirectory ") {
				return strings.TrimPrefix(tn, "ChrootDirectory "), true
			}
		}
	}
	return "", false
}

func siteuserTempSetup(t *testing.T, base string) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	if err := os.WriteFile(sshdFile, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osOpenFileFn = os.OpenFile
	execCommandFn = fakeExecCommand
	runtimeGOOS = "linux"
	return tmp, sshdFile
}

// TestCreateUserPrefixUsernameGetsOwnChroot pins F146: a user whose name is a
// prefix of an existing Match User (uwas-a--com vs uwas-a--com--tr) must still
// get its own chroot block; without it the account has unchrooted SFTP.
func TestCreateUserPrefixUsernameGetsOwnChroot(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	for _, order := range [][]string{{"a.com.tr", "a.com"}, {"a.com", "a.com.tr"}} {
		tmp, sshd := siteuserTempSetup(t, "Subsystem sftp internal-sftp\n")
		for _, h := range order {
			if _, _, err := CreateUser(tmp, h); err != nil {
				t.Fatalf("create %s: %v", h, err)
			}
		}
		data, _ := os.ReadFile(sshd)
		for _, h := range order {
			got, ok := chrootFor(string(data), domainToUsername(h))
			want := filepath.Join(tmp, h)
			if !ok || got != want {
				t.Errorf("order %v: chroot for %s = %q (found=%v), want %q", order, h, got, ok, want)
			}
		}
		// Re-creating must not duplicate either block.
		for _, h := range order {
			if _, _, err := CreateUser(tmp, h); err != nil {
				t.Fatal(err)
			}
		}
		data, _ = os.ReadFile(sshd)
		for _, h := range order {
			if n := strings.Count(string(data)+"\n", "Match User "+domainToUsername(h)+"\n"); n != 1 {
				t.Errorf("order %v: %d Match blocks for %s, want 1", order, n, h)
			}
		}
	}
}

// TestAddSSHKeyAfterUnterminatedLine pins F148: appending to an
// authorized_keys file whose last line has no newline must start a new line,
// not glue the new key into the previous key's comment.
func TestAddSSHKeyAfterUnterminatedLine(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	key := func(seed byte) string {
		raw := make([]byte, ed25519.PublicKeySize)
		for i := range raw {
			raw[i] = seed
		}
		pub, err := ssh.NewPublicKey(ed25519.PublicKey(raw))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	}
	k1, k2 := key(1), key(2)

	cases := map[string]string{
		"unterminated": k1 + " admin",
		"terminated":   k1 + " admin\n",
		"empty":        "",
	}
	for name, initial := range cases {
		tmp, _ := siteuserTempSetup(t, "")
		webDir := filepath.Join(tmp, "a.com", "public_html")
		authKeys := filepath.Join(tmp, "a.com", ".ssh", "authorized_keys")
		if err := os.MkdirAll(filepath.Dir(authKeys), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(authKeys, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := AddSSHKeyForWebDir(webDir, "a.com", k2); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := ListSSHKeysForWebDir(webDir, "a.com")
		var want []string
		if initial != "" {
			want = append(want, k1+" admin")
		}
		want = append(want, k2)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: keys = %q, want %q", name, got, want)
		}
		// Adding the same key again is a no-op.
		if err := AddSSHKeyForWebDir(webDir, "a.com", k2); err != nil {
			t.Fatal(err)
		}
		if again := ListSSHKeysForWebDir(webDir, "a.com"); len(again) != len(want) {
			t.Errorf("%s: duplicate add changed key count to %d", name, len(again))
		}
	}
}
