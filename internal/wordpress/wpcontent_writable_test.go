//go:build unix

package wordpress

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWPContentWritableJudgedForPHPAccount: Permissions.Writable means "PHP can
// write wp-content". When PHP runs as another account than UWAS (a root UWAS
// and the web account), it must be judged for that account, not by a test
// write as UWAS, which a root UWAS always passes (F68600).
func TestWPContentWritableJudgedForPHPAccount(t *testing.T) {
	saved := phpIdentityFn
	t.Cleanup(func() { phpIdentityFn = saved })

	site := t.TempDir()
	wpc := filepath.Join(site, "wp-content")
	if err := os.Mkdir(wpc, 0o755); err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Getuid(), os.Getgid()
	other := &runIdentity{uid: uid + 4242, gid: gid + 4242}
	sameGroup := &runIdentity{uid: uid + 4242, gid: gid}

	cases := []struct {
		name string
		id   *runIdentity
		mode os.FileMode
		want bool
	}{
		{"other account, 0755", other, 0o755, false},
		{"other account, 0757", other, 0o757, true},
		{"other account, 0756 (no search)", other, 0o756, false},
		{"same group, 0775", sameGroup, 0o775, true},
		{"same group, 0755", sameGroup, 0o755, false},
		{"owner, 0755", &runIdentity{uid: uid, gid: gid}, 0o755, true},
		{"owner, 0555", &runIdentity{uid: uid, gid: gid}, 0o555, false},
	}
	for _, tc := range cases {
		if err := os.Chmod(wpc, tc.mode); err != nil {
			t.Fatal(err)
		}
		id := tc.id
		phpIdentityFn = func() (*runIdentity, error) { return id, nil }
		if got := checkPermissions(site).Writable; got != tc.want {
			t.Errorf("%s: Writable = %v, want %v", tc.name, got, tc.want)
		}
		if ents, _ := os.ReadDir(wpc); len(ents) != 0 {
			t.Errorf("%s: judging another account's access wrote %d file(s) into wp-content", tc.name, len(ents))
		}
	}
	_ = os.Chmod(wpc, 0o755)
}
