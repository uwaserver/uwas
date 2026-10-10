package siteuser

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// heldReadGate holds the first limit reads of path open until release is
// closed, so a test can start a competing operation in the middle of an
// authorized_keys read-modify-write.
type heldReadGate struct {
	path    string
	limit   int32
	n       atomic.Int32
	held    chan struct{}
	release chan struct{}
}

func (g *heldReadGate) read(name string) ([]byte, error) {
	data, err := os.ReadFile(name)
	if name == g.path && g.n.Add(1) <= g.limit {
		g.held <- struct{}{}
		<-g.release
	}
	return data, err
}

// waitCompetitor returns once the competing call has finished, had its own
// read held, or is parked on a mutex inside fn.
func waitCompetitor(t *testing.T, g *heldReadGate, done <-chan struct{}, fn string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	buf := make([]byte, 1<<20)
	for {
		select {
		case <-done:
			return
		case <-g.held:
			return
		default:
		}
		n := runtime.Stack(buf, true)
		for _, gs := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(gs, fn) && strings.Contains(gs, "sync.(*Mutex).Lock") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("competing %s neither finished, read nor parked", fn)
		}
		runtime.Gosched()
	}
}

func testAuthorizedKey(t *testing.T, seed byte) (line, fingerprint string) {
	t.Helper()
	pub := make([]byte, ed25519.PublicKeySize)
	for i := range pub {
		pub[i] = seed
	}
	k, err := ssh.NewPublicKey(ed25519.PublicKey(pub))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))), ssh.FingerprintSHA256(k)
}

// TestAuthorizedKeysReadModifyWriteSerialized pins the serialization of the
// authorized_keys read-modify-write. Unlocked, two overlapping removals each
// rewrote the file from the same stale read, so the second writer restored
// the key the first had revoked; a removal overlapping an add dropped the
// just-added key.
func TestAuthorizedKeysReadModifyWriteSerialized(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)
	runtimeGOOS = "linux"
	execCommandFn = fakeExecCommand
	osMkdirAllFn = os.MkdirAll
	osWriteFileFn = os.WriteFile
	osOpenFileFn = os.OpenFile

	k1, fp1 := testAuthorizedKey(t, 1)
	k2, fp2 := testAuthorizedKey(t, 2)
	k3, _ := testAuthorizedKey(t, 3)

	setup := func(t *testing.T, keys ...string) (string, string) {
		tmp := t.TempDir()
		webDir := filepath.Join(tmp, "example.com", "public_html")
		authKeys := filepath.Join(tmp, "example.com", ".ssh", "authorized_keys")
		if err := os.MkdirAll(filepath.Dir(authKeys), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(authKeys, []byte(strings.Join(keys, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return webDir, authKeys
	}
	present := func(t *testing.T, path, key string) bool {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(l) == key {
				return true
			}
		}
		return false
	}

	t.Run("overlapping removals both revoke", func(t *testing.T) {
		webDir, authKeys := setup(t, k1, k2, k3)
		g := &heldReadGate{path: authKeys, limit: 2, held: make(chan struct{}, 2), release: make(chan struct{})}
		osReadFileFn = g.read
		defer func() { osReadFileFn = os.ReadFile }()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = RemoveSSHKeyForWebDir(webDir, "example.com", fp1) }()
		<-g.held
		done := make(chan struct{})
		go func() { defer wg.Done(); defer close(done); _ = RemoveSSHKeyForWebDir(webDir, "example.com", fp2) }()
		waitCompetitor(t, g, done, "RemoveSSHKeyForWebDir")
		close(g.release)
		wg.Wait()
		if present(t, authKeys, k1) || present(t, authKeys, k2) || !present(t, authKeys, k3) {
			t.Fatalf("after two overlapping removals: k1=%v k2=%v k3=%v, want false false true",
				present(t, authKeys, k1), present(t, authKeys, k2), present(t, authKeys, k3))
		}
	})

	t.Run("add during removal is kept", func(t *testing.T) {
		webDir, authKeys := setup(t, k1, k2)
		g := &heldReadGate{path: authKeys, limit: 1, held: make(chan struct{}, 2), release: make(chan struct{})}
		osReadFileFn = g.read
		defer func() { osReadFileFn = os.ReadFile }()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = RemoveSSHKeyForWebDir(webDir, "example.com", fp1) }()
		<-g.held
		done := make(chan struct{})
		go func() { defer wg.Done(); defer close(done); _ = AddSSHKeyForWebDir(webDir, "example.com", k3) }()
		waitCompetitor(t, g, done, "AddSSHKeyForWebDir")
		close(g.release)
		wg.Wait()
		if present(t, authKeys, k1) || !present(t, authKeys, k2) || !present(t, authKeys, k3) {
			t.Fatalf("after add during removal: k1=%v k2=%v k3=%v, want false true true",
				present(t, authKeys, k1), present(t, authKeys, k2), present(t, authKeys, k3))
		}
	})
}
