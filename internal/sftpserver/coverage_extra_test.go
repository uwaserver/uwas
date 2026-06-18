package sftpserver

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// comparePassword: legacy plaintext rejection (server.go:810) plus the valid
// and mismatch bcrypt paths.
// ---------------------------------------------------------------------------

func TestComparePassword_LegacyPlaintextRejected(t *testing.T) {
	// A non-bcrypt stored value must be rejected outright.
	if err := comparePassword("plaintextsecret", []byte("plaintextsecret")); err == nil {
		t.Fatal("expected legacy plaintext password to be rejected")
	}
}

func TestComparePassword_Bcrypt(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := comparePassword(string(hash), []byte("hunter2")); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	if err := comparePassword(string(hash), []byte("wrong")); err == nil {
		t.Fatal("expected mismatch error")
	}
}

// ---------------------------------------------------------------------------
// Start: loadOrGenerateHostKey fails because the host key file is corrupt
// (server.go:100-102).
// ---------------------------------------------------------------------------

func TestStart_HostKeyError(t *testing.T) {
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "bad_host_key")
	// Write garbage that ParsePrivateKey will reject.
	if err := os.WriteFile(keyPath, []byte("not a private key"), 0600); err != nil {
		t.Fatal(err)
	}

	s := New(Config{Listen: "127.0.0.1:0", HostKey: keyPath}, testLogger())
	if err := s.Start(); err == nil {
		s.Stop()
		t.Fatal("expected Start to fail with a corrupt host key")
	}
}

// ---------------------------------------------------------------------------
// loadOrGenerateHostKey: MkdirAll succeeds but WriteFile fails because the
// target path is an existing directory (server.go:792-794). The key is still
// returned (ephemeral) and no error is propagated.
// ---------------------------------------------------------------------------

func TestLoadOrGenerateHostKey_PersistWriteError(t *testing.T) {
	tmp := t.TempDir()
	// Make the key path itself a directory so os.WriteFile fails.
	keyPath := filepath.Join(tmp, "keydir")
	if err := os.Mkdir(keyPath, 0700); err != nil {
		t.Fatal(err)
	}

	s := New(Config{HostKey: keyPath}, testLogger())
	signer, err := s.loadOrGenerateHostKey()
	if err != nil {
		t.Fatalf("expected ephemeral key despite write failure, got error: %v", err)
	}
	if signer == nil {
		t.Fatal("expected a non-nil signer even when persistence fails")
	}
}

// ---------------------------------------------------------------------------
// handleFStat: the underlying file is closed, so file.Stat() returns an error
// (server.go:507-510). Driven directly against a sftpSession over a pipe.
// ---------------------------------------------------------------------------

func TestHandleFStat_StatError(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	// Open a real file then close it so Stat() on the *os.File fails.
	f, err := os.CreateTemp(t.TempDir(), "fstat")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(serverConn),
		handles: map[string]*openHandle{"h1": {path: f.Name(), file: f}},
	}

	// Read the server's response on the client side concurrently.
	type result struct {
		pktType byte
		payload []byte
	}
	resCh := make(chan result, 1)
	go func() {
		var lenBuf [4]byte
		if _, err := readFull(clientConn, lenBuf[:]); err != nil {
			resCh <- result{}
			return
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		buf := make([]byte, n)
		if _, err := readFull(clientConn, buf); err != nil {
			resCh <- result{}
			return
		}
		resCh <- result{pktType: buf[0], payload: buf[5:]}
	}()

	sess.handleFStat(99, marshalString("h1"))

	r := <-resCh
	if r.pktType != sshFXPStatus {
		t.Fatalf("expected status packet on stat error, got %d", r.pktType)
	}
	if statusCode(r.payload) != sshFXFailure {
		t.Errorf("expected FAILURE status, got %d", statusCode(r.payload))
	}
}

// readFull is a tiny io.ReadFull replacement to avoid an extra import in the
// goroutine above (kept local so the helper file stays self-contained).
func readFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
