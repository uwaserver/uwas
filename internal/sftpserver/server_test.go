package sftpserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"golang.org/x/crypto/ssh"
)

func testLogger() *logger.Logger {
	return logger.New("error", "text")
}

// =============================================================================
// Unit Tests: New, UpdateUsers
// =============================================================================

func TestNew(t *testing.T) {
	cfg := Config{
		Listen: ":0",
		Users: map[string]User{
			"alice": {Password: "pass1", Root: "/home/alice", ReadOnly: false},
			"bob":   {Password: "pass2", Root: "/home/bob", ReadOnly: true},
		},
	}
	s := New(cfg, testLogger())
	if s == nil {
		t.Fatal("New returned nil")
	}
	if len(s.users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(s.users))
	}
	if s.users["alice"].Password != "pass1" {
		t.Errorf("alice password mismatch")
	}
	if !s.users["bob"].ReadOnly {
		t.Errorf("bob should be read-only")
	}
}

func TestNew_EmptyUsers(t *testing.T) {
	cfg := Config{Listen: ":0"}
	s := New(cfg, testLogger())
	if s == nil {
		t.Fatal("New returned nil")
	}
	if len(s.users) != 0 {
		t.Fatalf("expected 0 users, got %d", len(s.users))
	}
}

func TestUpdateUsers(t *testing.T) {
	cfg := Config{
		Listen: ":0",
		Users: map[string]User{
			"alice": {Password: "pass1", Root: "/home/alice"},
		},
	}
	s := New(cfg, testLogger())
	if len(s.users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(s.users))
	}

	newUsers := map[string]User{
		"charlie": {Password: "pass3", Root: "/home/charlie"},
		"dave":    {Password: "pass4", Root: "/home/dave", ReadOnly: true},
	}
	s.UpdateUsers(newUsers)
	if len(s.users) != 2 {
		t.Fatalf("expected 2 users after update, got %d", len(s.users))
	}
	if _, ok := s.users["alice"]; ok {
		t.Errorf("alice should have been removed")
	}
	if s.users["charlie"].Password != "pass3" {
		t.Errorf("charlie password mismatch")
	}
	if !s.users["dave"].ReadOnly {
		t.Errorf("dave should be read-only")
	}
}

func TestUpdateUsers_Concurrent(t *testing.T) {
	cfg := Config{Listen: ":0"}
	s := New(cfg, testLogger())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.UpdateUsers(map[string]User{
				fmt.Sprintf("user%d", i): {Password: "p", Root: "/tmp"},
			})
		}(i)
	}
	wg.Wait()
	// Just ensure no race/panic; final state is non-deterministic.
}

// =============================================================================
// Unit Tests: safePath
// =============================================================================

func TestSafePath(t *testing.T) {
	root := t.TempDir()

	sess := &sftpSession{
		root:    root,
		handles: make(map[string]*openHandle),
	}

	// safePath rejects any input containing ".." to prevent traversal on all platforms.
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantRel string // expected path relative to root ("" = root itself)
	}{
		{"empty returns root", "", true, ""},
		{"dot returns root", ".", true, ""},
		{"simple relative", "foo.txt", true, "foo.txt"},
		{"nested path", "a/b/c", true, filepath.Join("a", "b", "c")},
		{"absolute within root", "/foo/bar", true, filepath.Join("foo", "bar")},
		// Traversal attempts: all rejected (contain "..")
		{"traversal leading slash", "/../../../etc/shadow", false, ""},
		{"traversal dot-dot", "../../etc/passwd", false, ""},
		{"traversal embedded", "foo/../../etc/passwd", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sess.safePath(tt.input)
			if !tt.wantOK {
				if result != "" {
					t.Errorf("expected empty for traversal, got %q", result)
				}
				return
			}
			if result == "" {
				t.Fatalf("expected non-empty result")
			}
			absRoot, _ := filepath.Abs(root)
			expected := absRoot
			if tt.wantRel != "" {
				expected = filepath.Join(absRoot, tt.wantRel)
			}
			if result != expected {
				t.Errorf("got %q, want %q", result, expected)
			}
		})
	}
}

// =============================================================================
// Unit Tests: readString, encodeAttrs, encodeName
// =============================================================================

func TestReadString(t *testing.T) {
	// Normal case
	payload := make([]byte, 4+5)
	binary.BigEndian.PutUint32(payload[0:4], 5)
	copy(payload[4:], "hello")
	s, rest := readString(payload)
	if s != "hello" {
		t.Errorf("expected %q, got %q", "hello", s)
	}
	if len(rest) != 0 {
		t.Errorf("expected empty rest, got %d bytes", len(rest))
	}

	// With trailing data
	payload2 := make([]byte, 4+3+10)
	binary.BigEndian.PutUint32(payload2[0:4], 3)
	copy(payload2[4:], "abcEXTRADATA")
	s2, rest2 := readString(payload2)
	if s2 != "abc" {
		t.Errorf("expected %q, got %q", "abc", s2)
	}
	if len(rest2) != 10 {
		t.Errorf("expected 10 bytes rest, got %d", len(rest2))
	}

	// Too short (< 4 bytes)
	s3, rest3 := readString([]byte{0, 1})
	if s3 != "" || rest3 != nil {
		t.Errorf("expected empty string and nil rest for short buffer")
	}

	// Length exceeds buffer
	short := make([]byte, 4)
	binary.BigEndian.PutUint32(short[0:4], 100)
	s4, rest4 := readString(short)
	if s4 != "" || rest4 != nil {
		t.Errorf("expected empty string and nil rest for truncated buffer")
	}

	// Empty string
	empty := make([]byte, 4)
	binary.BigEndian.PutUint32(empty[0:4], 0)
	s5, rest5 := readString(empty)
	if s5 != "" {
		t.Errorf("expected empty string, got %q", s5)
	}
	if rest5 == nil {
		t.Errorf("expected non-nil rest for empty string")
	}
}

func TestEncodeAttrs(t *testing.T) {
	// Create a real file for a real os.FileInfo
	tmp := t.TempDir()
	fpath := filepath.Join(tmp, "test.txt")
	if err := os.WriteFile(fpath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(fpath)
	if err != nil {
		t.Fatal(err)
	}

	attrs := encodeAttrs(info)
	if len(attrs) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(attrs))
	}

	// Check flags
	flags := binary.BigEndian.Uint32(attrs[0:4])
	if flags != 0x0000000F {
		t.Errorf("expected flags 0x0F, got 0x%08X", flags)
	}

	// Check size
	size := binary.BigEndian.Uint64(attrs[4:12])
	if size != uint64(info.Size()) {
		t.Errorf("expected size %d, got %d", info.Size(), size)
	}

	// Check permissions
	perm := binary.BigEndian.Uint32(attrs[20:24])
	if perm != uint32(info.Mode()) {
		t.Errorf("expected mode %o, got %o", info.Mode(), perm)
	}

	// Check mtime
	mtime := binary.BigEndian.Uint32(attrs[28:32])
	if mtime != uint32(info.ModTime().Unix()) {
		t.Errorf("expected mtime %d, got %d", info.ModTime().Unix(), mtime)
	}
}

func TestEncodeName(t *testing.T) {
	tmp := t.TempDir()
	fpath := filepath.Join(tmp, "file.txt")
	os.WriteFile(fpath, []byte("data"), 0644)
	info, _ := os.Stat(fpath)

	data := encodeName("file.txt", info)
	// Parse the name back
	name, rest := readString(data)
	if name != "file.txt" {
		t.Errorf("expected name %q, got %q", "file.txt", name)
	}
	// Parse longname
	longname, rest2 := readString(rest)
	if longname == "" {
		t.Errorf("expected non-empty longname")
	}
	if !strings.Contains(longname, "file.txt") {
		t.Errorf("longname should contain filename, got %q", longname)
	}
	// rest2 should be the attrs (32 bytes)
	if len(rest2) != 32 {
		t.Errorf("expected 32 bytes of attrs, got %d", len(rest2))
	}
}

// =============================================================================
// Unit Tests: newHandle
// =============================================================================

func TestNewHandle(t *testing.T) {
	sess := &sftpSession{
		handles: make(map[string]*openHandle),
	}
	h1 := sess.newHandle(&openHandle{path: "/tmp/a"})
	h2 := sess.newHandle(&openHandle{path: "/tmp/b"})
	if h1 == h2 {
		t.Errorf("handles should be unique")
	}
	if len(sess.handles) != 2 {
		t.Errorf("expected 2 handles, got %d", len(sess.handles))
	}
	if sess.handles[h1].path != "/tmp/a" {
		t.Errorf("h1 path mismatch")
	}
	if sess.handles[h2].path != "/tmp/b" {
		t.Errorf("h2 path mismatch")
	}
}

// =============================================================================
// Host Key Tests
// =============================================================================

func TestHostKey_Generate(t *testing.T) {
	cfg := Config{
		Listen:  ":0",
		HostKey: filepath.Join(t.TempDir(), "nonexistent", "host_key"),
	}
	s := New(cfg, testLogger())
	signer, err := s.loadOrGenerateHostKey()
	if err != nil {
		t.Fatalf("expected key generation to succeed: %v", err)
	}
	if signer == nil {
		t.Fatal("expected non-nil signer")
	}
}

func TestHostKey_Load(t *testing.T) {
	// Generate an ed25519 key, save it to disk in PEM format, then verify
	// loadOrGenerateHostKey loads it successfully.
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "host_key")

	// Generate a raw ed25519 private key
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	// Create signer from raw key, then marshal to PEM
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	// ssh.MarshalPrivateKey wants the raw crypto key, not the signer
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pemData := pem.EncodeToMemory(pemBlock)
	if err := os.WriteFile(keyPath, pemData, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// Now loadOrGenerateHostKey should read from file
	cfg := Config{Listen: ":0", HostKey: keyPath}
	s := New(cfg, testLogger())
	signer2, err := s.loadOrGenerateHostKey()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if signer2 == nil {
		t.Fatal("expected non-nil signer from loaded key")
	}
	// The loaded key should have the same public key
	pub1 := signer.PublicKey().Marshal()
	pub2 := signer2.PublicKey().Marshal()
	if !bytes.Equal(pub1, pub2) {
		t.Error("loaded key should match generated key")
	}
}

// =============================================================================
// Integration Tests: Start/Stop, SSH Auth
// =============================================================================

func startTestServer(t *testing.T, users map[string]User) (*Server, string) {
	t.Helper()
	cfg := Config{
		Listen:  "127.0.0.1:0",
		HostKey: filepath.Join(t.TempDir(), "host_key"),
		Users:   users,
	}
	s := New(cfg, testLogger())
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := s.listener.Addr().String()
	return s, addr
}

func sshClientConfig(user, pass string) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
}

func TestStartStop(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"test": {Password: "secret", Root: root},
	})
	defer s.Stop()

	// Verify we can connect at the TCP level
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	// Stop and verify we can't connect anymore
	s.Stop()
	_, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		t.Errorf("expected dial to fail after Stop")
	}
}

func TestStop_Nil(t *testing.T) {
	cfg := Config{Listen: ":0"}
	s := New(cfg, testLogger())
	// Stop on a server that was never started should not panic
	s.Stop()
}

func TestSSHAuth_ValidPassword(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"alice": {Password: "correct", Root: root},
	})
	defer s.Stop()

	clientCfg := sshClientConfig("alice", "correct")
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	client.Close()
}

func TestSSHAuth_InvalidPassword(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"alice": {Password: "correct", Root: root},
	})
	defer s.Stop()

	clientCfg := sshClientConfig("alice", "wrong")
	_, err := ssh.Dial("tcp", addr, clientCfg)
	if err == nil {
		t.Fatal("expected auth failure with wrong password")
	}
}

func TestSSHAuth_UnknownUser(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"alice": {Password: "correct", Root: root},
	})
	defer s.Stop()

	clientCfg := sshClientConfig("unknown", "pass")
	_, err := ssh.Dial("tcp", addr, clientCfg)
	if err == nil {
		t.Fatal("expected auth failure for unknown user")
	}
}

// =============================================================================
// SFTP Protocol Tests (via SSH session + raw SFTP packets)
// =============================================================================

// sftpClient wraps an SSH channel for sending/receiving raw SFTP packets.
type sftpClient struct {
	ch ssh.Channel
}

func openSFTPSession(t *testing.T, addr, user, pass string) (*ssh.Client, *sftpClient) {
	t.Helper()
	clientCfg := sshClientConfig(user, pass)
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	session, reqs, err := client.OpenChannel("session", nil)
	if err != nil {
		client.Close()
		t.Fatalf("open channel: %v", err)
	}
	go ssh.DiscardRequests(reqs)

	// Request sftp subsystem
	ok, err := session.SendRequest("subsystem", true, append(marshalUint32(4), []byte("sftp")...))
	if err != nil {
		session.Close()
		client.Close()
		t.Fatalf("subsystem request: %v", err)
	}
	if !ok {
		session.Close()
		client.Close()
		t.Fatal("subsystem request rejected")
	}

	return client, &sftpClient{ch: session}
}

func marshalUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func marshalUint64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func marshalString(s string) []byte {
	b := make([]byte, 4+len(s))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(s)))
	copy(b[4:], s)
	return b
}

// sendPacket sends a raw SFTP packet (type + id + data).
func (c *sftpClient) sendPacket(pktType byte, id uint32, data []byte) error {
	total := 1 + 4 + len(data)
	buf := make([]byte, 4+total)
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	buf[4] = pktType
	binary.BigEndian.PutUint32(buf[5:9], id)
	copy(buf[9:], data)
	_, err := c.ch.Write(buf)
	return err
}

// sendInit sends SSH_FXP_INIT with version 3.
func (c *sftpClient) sendInit() error {
	total := 1 + 4 // type + version
	buf := make([]byte, 4+total)
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	buf[4] = sshFXPInit
	binary.BigEndian.PutUint32(buf[5:9], 3) // version 3
	_, err := c.ch.Write(buf)
	return err
}

// recvPacket reads a raw SFTP response packet.
func (c *sftpClient) recvPacket() (pktType byte, id uint32, payload []byte, err error) {
	var lenBuf [4]byte
	if _, err = io.ReadFull(c.ch, lenBuf[:]); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, length)
	if _, err = io.ReadFull(c.ch, buf); err != nil {
		return
	}
	pktType = buf[0]
	if pktType == sshFXPVersion {
		// Version has no id field
		return pktType, 0, buf[1:], nil
	}
	if len(buf) < 5 {
		err = fmt.Errorf("packet too short")
		return
	}
	id = binary.BigEndian.Uint32(buf[1:5])
	payload = buf[5:]
	return
}

// statusCode parses a status payload and returns the code.
func statusCode(payload []byte) uint32 {
	if len(payload) < 4 {
		return 0xFFFFFFFF
	}
	return binary.BigEndian.Uint32(payload[0:4])
}

func TestSFTP_Init(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	if err := sftp.sendInit(); err != nil {
		t.Fatalf("sendInit: %v", err)
	}

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPVersion {
		t.Fatalf("expected SSH_FXP_VERSION (%d), got %d", sshFXPVersion, pktType)
	}
	if len(payload) < 4 {
		t.Fatal("version payload too short")
	}
	ver := binary.BigEndian.Uint32(payload[0:4])
	if ver != 3 {
		t.Errorf("expected SFTP version 3, got %d", ver)
	}
}

func TestSFTP_RealPath(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	// Init first
	sftp.sendInit()
	sftp.recvPacket()

	// Send realpath for "."
	sftp.sendPacket(sshFXPRealPath, 1, marshalString("."))
	pktType, id, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPName {
		t.Fatalf("expected SSH_FXP_NAME (%d), got %d", sshFXPName, pktType)
	}
	if id != 1 {
		t.Errorf("expected id 1, got %d", id)
	}
	// Parse count + first name
	if len(payload) < 4 {
		t.Fatal("payload too short")
	}
	count := binary.BigEndian.Uint32(payload[0:4])
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
	name, _ := readString(payload[4:])
	if name != "/" {
		t.Errorf("expected path %q, got %q", "/", name)
	}
}

func TestSFTP_Stat(t *testing.T) {
	root := t.TempDir()
	// Create a file
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Stat existing file
	sftp.sendPacket(sshFXPStat, 2, marshalString("test.txt"))
	pktType, id, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPAttrs {
		t.Fatalf("expected SSH_FXP_ATTRS (%d), got %d", sshFXPAttrs, pktType)
	}
	if id != 2 {
		t.Errorf("expected id 2, got %d", id)
	}
	// Check size in attrs
	if len(payload) < 12 {
		t.Fatal("attrs too short")
	}
	size := binary.BigEndian.Uint64(payload[4:12])
	if size != 5 {
		t.Errorf("expected size 5, got %d", size)
	}
}

func TestSFTP_Stat_NotFound(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPStat, 3, marshalString("nonexistent.txt"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected SSH_FXP_STATUS (%d), got %d", sshFXPStatus, pktType)
	}
	code := statusCode(payload)
	if code != sshFXNoSuchFile {
		t.Errorf("expected SSH_FX_NO_SUCH_FILE (%d), got %d", sshFXNoSuchFile, code)
	}
}

func TestSFTP_LStat(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("data"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPLStat, 10, marshalString("file.txt"))
	pktType, _, _, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	// LStat dispatches to the same handler as Stat, so we expect ATTRS.
	if pktType != sshFXPAttrs {
		t.Fatalf("expected SSH_FXP_ATTRS (%d), got %d", sshFXPAttrs, pktType)
	}
}

func TestSFTP_OpenDir_ReadDir(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("bbb"), 0644)
	os.Mkdir(filepath.Join(root, "subdir"), 0755)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// OpenDir
	sftp.sendPacket(sshFXPOpenDir, 4, marshalString("."))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected SSH_FXP_HANDLE (%d), got %d", sshFXPHandle, pktType)
	}
	handle, _ := readString(payload)
	if handle == "" {
		t.Fatal("expected non-empty handle")
	}

	// ReadDir (first call returns entries)
	sftp.sendPacket(sshFXPReadDir, 5, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPName {
		t.Fatalf("expected SSH_FXP_NAME (%d), got %d", sshFXPName, pktType)
	}
	count := binary.BigEndian.Uint32(payload[0:4])
	if count != 3 {
		t.Errorf("expected 3 entries, got %d", count)
	}

	// ReadDir again (should return EOF)
	sftp.sendPacket(sshFXPReadDir, 6, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected SSH_FXP_STATUS (%d), got %d", sshFXPStatus, pktType)
	}
	if statusCode(payload) != sshFXEOF {
		t.Errorf("expected EOF, got code %d", statusCode(payload))
	}

	// Close handle
	sftp.sendPacket(sshFXPClose, 7, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Errorf("expected OK status for close")
	}
}

func TestSFTP_OpenDir_NotADir(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPOpenDir, 1, marshalString("file.txt"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXNoSuchFile {
		t.Errorf("expected SSH_FX_NO_SUCH_FILE, got %d", statusCode(payload))
	}
}

func TestSFTP_Open_Read(t *testing.T) {
	root := t.TempDir()
	content := "hello sftp world!"
	os.WriteFile(filepath.Join(root, "test.txt"), []byte(content), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for reading: path + pflags(4) + attrs(4)
	openPayload := marshalString("test.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...) // attrs flags = 0
	sftp.sendPacket(sshFXPOpen, 10, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected SSH_FXP_HANDLE (%d), got %d", sshFXPHandle, pktType)
	}
	handle, _ := readString(payload)

	// Read data: handle + offset(8) + length(4)
	readPayload := marshalString(handle)
	readPayload = append(readPayload, marshalUint64(0)...)
	readPayload = append(readPayload, marshalUint32(1024)...)
	sftp.sendPacket(sshFXPRead, 11, readPayload)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPData {
		t.Fatalf("expected SSH_FXP_DATA (%d), got %d", sshFXPData, pktType)
	}
	// Parse data: length(4) + data
	if len(payload) < 4 {
		t.Fatal("data payload too short")
	}
	dataLen := binary.BigEndian.Uint32(payload[0:4])
	data := string(payload[4 : 4+dataLen])
	if data != content {
		t.Errorf("expected %q, got %q", content, data)
	}

	// Read past EOF
	readPayload2 := marshalString(handle)
	readPayload2 = append(readPayload2, marshalUint64(uint64(len(content)))...)
	readPayload2 = append(readPayload2, marshalUint32(1024)...)
	sftp.sendPacket(sshFXPRead, 12, readPayload2)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected SSH_FXP_STATUS, got %d", pktType)
	}
	if statusCode(payload) != sshFXEOF {
		t.Errorf("expected EOF, got %d", statusCode(payload))
	}

	// Close
	sftp.sendPacket(sshFXPClose, 13, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_Open_Write(t *testing.T) {
	root := t.TempDir()

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for write + create + truncate
	pflags := sshFXFWrite | sshFXFCreat | sshFXFTrunc
	openPayload := marshalString("new.txt")
	openPayload = append(openPayload, marshalUint32(uint32(pflags))...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 20, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected SSH_FXP_HANDLE (%d), got %d", sshFXPHandle, pktType)
	}
	handle, _ := readString(payload)

	// Write data
	content := []byte("written via SFTP")
	writePayload := marshalString(handle)
	writePayload = append(writePayload, marshalUint64(0)...)
	writePayload = append(writePayload, marshalUint32(uint32(len(content)))...)
	writePayload = append(writePayload, content...)
	sftp.sendPacket(sshFXPWrite, 21, writePayload)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Fatalf("expected OK status for write, got type=%d code=%d", pktType, statusCode(payload))
	}

	// Close
	sftp.sendPacket(sshFXPClose, 22, marshalString(handle))
	sftp.recvPacket()

	// Verify the file on disk
	got, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "written via SFTP" {
		t.Errorf("expected %q, got %q", "written via SFTP", string(got))
	}
}

func TestSFTP_FStat(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "stat.txt"), []byte("12345"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for reading
	openPayload := marshalString("stat.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 30, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// FStat
	sftp.sendPacket(sshFXPFStat, 31, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPAttrs {
		t.Fatalf("expected SSH_FXP_ATTRS, got %d", pktType)
	}
	size := binary.BigEndian.Uint64(payload[4:12])
	if size != 5 {
		t.Errorf("expected size 5, got %d", size)
	}

	// Close
	sftp.sendPacket(sshFXPClose, 32, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_FStat_InvalidHandle(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPFStat, 40, marshalString("bogus_handle"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE, got %d", statusCode(payload))
	}
}

func TestSFTP_MkDir(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// MkDir: path + attrs
	mkdirPayload := marshalString("newdir")
	mkdirPayload = append(mkdirPayload, marshalUint32(0)...) // attrs
	sftp.sendPacket(sshFXPMkDir, 50, mkdirPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Fatalf("expected OK for mkdir, got type=%d code=%d", pktType, statusCode(payload))
	}

	// Verify directory exists
	info, err := os.Stat(filepath.Join(root, "newdir"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("expected directory")
	}
}

func TestSFTP_RmDir(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "rmme"), 0755)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRmDir, 51, marshalString("rmme"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Fatalf("expected OK for rmdir, got type=%d code=%d", pktType, statusCode(payload))
	}

	if _, err := os.Stat(filepath.Join(root, "rmme")); !os.IsNotExist(err) {
		t.Errorf("directory should have been removed")
	}
}

func TestSFTP_RmDir_Root(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Attempt to rmdir the root itself ("." resolves to root)
	sftp.sendPacket(sshFXPRmDir, 52, marshalString("."))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED for rmdir root, got %d", statusCode(payload))
	}
}

func TestSFTP_Remove(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "delete.txt"), []byte("bye"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRemove, 60, marshalString("delete.txt"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Fatalf("expected OK for remove")
	}

	if _, err := os.Stat(filepath.Join(root, "delete.txt")); !os.IsNotExist(err) {
		t.Errorf("file should have been removed")
	}
}

func TestSFTP_Remove_Root(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRemove, 61, marshalString("."))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED for remove root, got %d", statusCode(payload))
	}
}

func TestSFTP_Remove_NotFound(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRemove, 62, marshalString("ghost.txt"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for non-existent file, got %d", statusCode(payload))
	}
}

func TestSFTP_Rename(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "old.txt"), []byte("rename me"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	renamePayload := marshalString("old.txt")
	renamePayload = append(renamePayload, marshalString("new.txt")...)
	sftp.sendPacket(sshFXPRename, 70, renamePayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Fatalf("expected OK for rename")
	}

	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old file should not exist")
	}
	data, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "rename me" {
		t.Errorf("expected %q, got %q", "rename me", string(data))
	}
}

func TestSFTP_SetStat(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// SetStat always returns OK
	setStatPayload := marshalString("file.txt")
	setStatPayload = append(setStatPayload, marshalUint32(0)...) // empty attrs
	sftp.sendPacket(sshFXPSetStat, 80, setStatPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Fatalf("expected OK for setstat")
	}
}

func TestSFTP_UnsupportedPacket(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Send an unsupported packet type (200)
	sftp.sendPacket(200, 99, nil)
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status for unsupported, got %d", pktType)
	}
	if statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE, got %d", statusCode(payload))
	}
}

// =============================================================================
// Read-Only Mode Tests
// =============================================================================

func TestSFTP_ReadOnly_Write(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "existing.txt"), []byte("data"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root, ReadOnly: true},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Try to open for writing
	pflags := sshFXFWrite | sshFXFCreat | sshFXFTrunc
	openPayload := marshalString("new.txt")
	openPayload = append(openPayload, marshalUint32(uint32(pflags))...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 90, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status for read-only write attempt, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_ReadOnly_Read(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "readable.txt"), []byte("can read"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root, ReadOnly: true},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for reading should work even in read-only mode
	openPayload := marshalString("readable.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 91, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle for read in read-only mode, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Close
	sftp.sendPacket(sshFXPClose, 92, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_ReadOnly_Remove(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root, ReadOnly: true},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRemove, 93, marshalString("keep.txt"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED for read-only remove")
	}

	// Verify file still exists
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Errorf("file should still exist")
	}
}

func TestSFTP_ReadOnly_MkDir(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root, ReadOnly: true},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	mkdirPayload := marshalString("nope")
	mkdirPayload = append(mkdirPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPMkDir, 94, mkdirPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED for read-only mkdir")
	}
}

func TestSFTP_ReadOnly_RmDir(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "keepdir"), 0755)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root, ReadOnly: true},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRmDir, 95, marshalString("keepdir"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED for read-only rmdir")
	}
}

func TestSFTP_ReadOnly_Rename(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "stay.txt"), []byte("stay"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root, ReadOnly: true},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	renamePayload := marshalString("stay.txt")
	renamePayload = append(renamePayload, marshalString("moved.txt")...)
	sftp.sendPacket(sshFXPRename, 96, renamePayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED for read-only rename")
	}
}

// =============================================================================
// Path Traversal Protection Tests
// =============================================================================

func TestSFTP_PathTraversal_Stat(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Try path traversal
	sftp.sendPacket(sshFXPStat, 100, marshalString("../../etc/passwd"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status for traversal, got %d", pktType)
	}
	code := statusCode(payload)
	// On Unix: PERMISSION_DENIED (path escapes root).
	// On Windows: NO_SUCH_FILE (path resolves within root but doesn't exist).
	if code != sshFXPermissionDenied && code != sshFXNoSuchFile {
		t.Errorf("expected PERMISSION_DENIED or NO_SUCH_FILE, got %d", code)
	}
}

func TestSFTP_PathTraversal_Open(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	openPayload := marshalString("/../../../etc/shadow")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 101, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status for traversal, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_PathTraversal_OpenDir(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// "../../../" contains ".." -> PERMISSION_DENIED on all platforms.
	sftp.sendPacket(sshFXPOpenDir, 102, marshalString("../../../"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status for traversal, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_PathTraversal_MkDir(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	mkdirPayload := marshalString("../../evil")
	mkdirPayload = append(mkdirPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPMkDir, 103, mkdirPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status for traversal, got %d", pktType)
	}
	code := statusCode(payload)
	// Path contains ".." -> PERMISSION_DENIED on all platforms.
	if code != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", code)
	}
}

func TestSFTP_PathTraversal_Rename(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Try to rename to a path outside chroot
	renamePayload := marshalString("a.txt")
	renamePayload = append(renamePayload, marshalString("../../escape.txt")...)
	sftp.sendPacket(sshFXPRename, 104, renamePayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status for traversal, got %d", pktType)
	}
	code := statusCode(payload)
	// Path contains ".." -> PERMISSION_DENIED on all platforms.
	if code != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", code)
	}
}

// =============================================================================
// Edge Cases: invalid handle, bad packets
// =============================================================================

func TestSFTP_ReadDir_InvalidHandle(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPReadDir, 110, marshalString("bogus"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for invalid readdir handle")
	}
}

func TestSFTP_Read_InvalidHandle(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	readPayload := marshalString("bogus")
	readPayload = append(readPayload, marshalUint64(0)...)
	readPayload = append(readPayload, marshalUint32(1024)...)
	sftp.sendPacket(sshFXPRead, 111, readPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for invalid read handle")
	}
}

func TestSFTP_Write_InvalidHandle(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	writePayload := marshalString("bogus")
	writePayload = append(writePayload, marshalUint64(0)...)
	writePayload = append(writePayload, marshalUint32(4)...)
	writePayload = append(writePayload, []byte("data")...)
	sftp.sendPacket(sshFXPWrite, 112, writePayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for invalid write handle")
	}
}

func TestSFTP_Open_BadPacket(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Send open with path but no pflags/attrs (too short rest)
	sftp.sendPacket(sshFXPOpen, 120, marshalString("x"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for bad open packet")
	}
}

func TestSFTP_Read_BadPacket(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("data"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open file to get valid handle
	openPayload := marshalString("f.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 130, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Send read with handle but no offset/length (too short)
	sftp.sendPacket(sshFXPRead, 131, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for bad read packet")
	}

	// Close
	sftp.sendPacket(sshFXPClose, 132, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_Write_BadPacket(t *testing.T) {
	root := t.TempDir()

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open file to get valid handle
	pflags := sshFXFWrite | sshFXFCreat | sshFXFTrunc
	openPayload := marshalString("w.txt")
	openPayload = append(openPayload, marshalUint32(uint32(pflags))...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 140, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Write with handle but no offset/length (too short)
	sftp.sendPacket(sshFXPWrite, 141, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for bad write packet")
	}

	// Write with short data
	writePayload := marshalString(handle)
	writePayload = append(writePayload, marshalUint64(0)...)
	writePayload = append(writePayload, marshalUint32(100)...) // says 100 bytes
	writePayload = append(writePayload, []byte("short")...)    // but only 5
	sftp.sendPacket(sshFXPWrite, 142, writePayload)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for short data")
	}

	// Close
	sftp.sendPacket(sshFXPClose, 143, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_Close_UnknownHandle(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Close with a handle that doesn't exist — should still return OK
	sftp.sendPacket(sshFXPClose, 150, marshalString("nonexistent"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Errorf("expected OK for close of unknown handle")
	}
}

// =============================================================================
// Open with Append flag
// =============================================================================

func TestSFTP_Open_Append(t *testing.T) {
	// On Windows, WriteAt + O_APPEND can behave unexpectedly, so this test
	// only verifies the open succeeds with the append flag and the handle
	// can be closed cleanly.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "append.txt"), []byte("hello"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for write + append
	pflags := sshFXFWrite | sshFXFAppend
	openPayload := marshalString("append.txt")
	openPayload = append(openPayload, marshalUint32(uint32(pflags))...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 160, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got type %d code %d", pktType, statusCode(payload))
	}
	handle, _ := readString(payload)

	// Close the handle
	sftp.sendPacket(sshFXPClose, 162, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXOK {
		t.Errorf("expected OK for close")
	}
}

// =============================================================================
// Open with ReadWrite flag
// =============================================================================

func TestSFTP_Open_ReadWrite(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "rw.txt"), []byte("original"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for read + write
	pflags := sshFXFRead | sshFXFWrite
	openPayload := marshalString("rw.txt")
	openPayload = append(openPayload, marshalUint32(uint32(pflags))...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 170, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Read first
	readPayload := marshalString(handle)
	readPayload = append(readPayload, marshalUint64(0)...)
	readPayload = append(readPayload, marshalUint32(1024)...)
	sftp.sendPacket(sshFXPRead, 171, readPayload)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPData {
		t.Fatalf("expected data, got %d", pktType)
	}
	dataLen := binary.BigEndian.Uint32(payload[0:4])
	data := string(payload[4 : 4+dataLen])
	if data != "original" {
		t.Errorf("expected %q, got %q", "original", data)
	}

	// Close
	sftp.sendPacket(sshFXPClose, 172, marshalString(handle))
	sftp.recvPacket()
}

// =============================================================================
// Open nonexistent file for reading
// =============================================================================

func TestSFTP_Open_NotFound(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	openPayload := marshalString("missing.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 180, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXNoSuchFile {
		t.Errorf("expected NO_SUCH_FILE, got %d", statusCode(payload))
	}
}

// =============================================================================
// Packet I/O Tests via net.Pipe
// =============================================================================

func TestReadPacket_Init(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	// Write an INIT packet on the client side
	go func() {
		// length(4) + type(1) + version(4) = 5 bytes payload
		buf := make([]byte, 9)
		binary.BigEndian.PutUint32(buf[0:4], 5) // length
		buf[4] = sshFXPInit
		binary.BigEndian.PutUint32(buf[5:9], 3) // version
		client.Write(buf)
	}()

	pktType, id, payload, err := sess.readPacket()
	if err != nil {
		t.Fatalf("readPacket: %v", err)
	}
	if pktType != sshFXPInit {
		t.Errorf("expected INIT, got %d", pktType)
	}
	if id != 0 {
		t.Errorf("INIT should have id 0, got %d", id)
	}
	if len(payload) != 4 {
		t.Errorf("expected 4 byte payload (version), got %d", len(payload))
	}
}

func TestReadPacket_Regular(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	go func() {
		// A STAT packet: type(1) + id(4) + string data
		pathData := marshalString("/foo")
		total := 1 + 4 + len(pathData)
		buf := make([]byte, 4+total)
		binary.BigEndian.PutUint32(buf[0:4], uint32(total))
		buf[4] = sshFXPStat
		binary.BigEndian.PutUint32(buf[5:9], 42)
		copy(buf[9:], pathData)
		client.Write(buf)
	}()

	pktType, id, payload, err := sess.readPacket()
	if err != nil {
		t.Fatalf("readPacket: %v", err)
	}
	if pktType != sshFXPStat {
		t.Errorf("expected STAT, got %d", pktType)
	}
	if id != 42 {
		t.Errorf("expected id 42, got %d", id)
	}
	path, _ := readString(payload)
	if path != "/foo" {
		t.Errorf("expected path %q, got %q", "/foo", path)
	}
}

func TestReadPacket_TooLarge(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	go func() {
		// Send a packet claiming to be > 16MB
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], 1<<24+1)
		client.Write(buf[:])
		client.Close() // close so readPacket doesn't block
	}()

	_, _, _, err := sess.readPacket()
	if err == nil {
		t.Error("expected error for oversized packet")
	}
}

func TestReadPacket_EOF(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	// Close immediately to simulate EOF
	client.Close()

	_, _, _, err := sess.readPacket()
	if err == nil {
		t.Error("expected error on EOF")
	}
}

func TestWritePacket(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		n, _ := client.Read(buf)
		done <- buf[:n]
	}()

	sess.writePacket(sshFXPAttrs, 7, []byte{0x01, 0x02, 0x03})

	got := <-done
	// Expected: length(4) + type(1) + id(4) + data(3) = total 8 payload, 12 bytes
	if len(got) < 12 {
		t.Fatalf("expected at least 12 bytes, got %d", len(got))
	}
	length := binary.BigEndian.Uint32(got[0:4])
	if length != 8 { // 1 + 4 + 3
		t.Errorf("expected length 8, got %d", length)
	}
	if got[4] != sshFXPAttrs {
		t.Errorf("expected type %d, got %d", sshFXPAttrs, got[4])
	}
	id := binary.BigEndian.Uint32(got[5:9])
	if id != 7 {
		t.Errorf("expected id 7, got %d", id)
	}
	if !bytes.Equal(got[9:12], []byte{0x01, 0x02, 0x03}) {
		t.Errorf("data mismatch")
	}
}

func TestSendVersion(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		n, _ := client.Read(buf)
		done <- buf[:n]
	}()

	sess.sendVersion()

	got := <-done
	// Expected: 9 bytes total: length(4) + type(1) + version(4)
	if len(got) != 9 {
		t.Fatalf("expected 9 bytes, got %d", len(got))
	}
	length := binary.BigEndian.Uint32(got[0:4])
	if length != 5 {
		t.Errorf("expected length 5, got %d", length)
	}
	if got[4] != sshFXPVersion {
		t.Errorf("expected VERSION type, got %d", got[4])
	}
	ver := binary.BigEndian.Uint32(got[5:9])
	if ver != 3 {
		t.Errorf("expected version 3, got %d", ver)
	}
}

func TestSendStatus(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		n, _ := client.Read(buf)
		done <- buf[:n]
	}()

	sess.sendStatus(5, sshFXNoSuchFile, "not found")

	got := <-done
	// Parse: length(4) + type(1) + id(4) + code(4) + msglen(4) + msg + langlen(4)
	if len(got) < 9 {
		t.Fatalf("too short: %d bytes", len(got))
	}
	if got[4] != sshFXPStatus {
		t.Errorf("expected STATUS type, got %d", got[4])
	}
	id := binary.BigEndian.Uint32(got[5:9])
	if id != 5 {
		t.Errorf("expected id 5, got %d", id)
	}
	code := binary.BigEndian.Uint32(got[9:13])
	if code != sshFXNoSuchFile {
		t.Errorf("expected code %d, got %d", sshFXNoSuchFile, code)
	}
	msg, rest := readString(got[13:])
	if msg != "not found" {
		t.Errorf("expected msg %q, got %q", "not found", msg)
	}
	// lang tag (empty string = 4 zero bytes)
	if len(rest) < 4 {
		t.Errorf("expected lang tag bytes")
	}
}

func TestSendHandle(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		n, _ := client.Read(buf)
		done <- buf[:n]
	}()

	sess.sendHandle(9, "h42")

	got := <-done
	// Parse: length(4) + type(1) + id(4) + handlelen(4) + handle
	if got[4] != sshFXPHandle {
		t.Errorf("expected HANDLE type, got %d", got[4])
	}
	id := binary.BigEndian.Uint32(got[5:9])
	if id != 9 {
		t.Errorf("expected id 9, got %d", id)
	}
	handle, _ := readString(got[9:])
	if handle != "h42" {
		t.Errorf("expected handle %q, got %q", "h42", handle)
	}
}

// =============================================================================
// Marshal/Parse helpers
// =============================================================================

func TestMarshalUint32(t *testing.T) {
	b := marshalUint32(0x12345678)
	if len(b) != 4 {
		t.Fatalf("expected 4 bytes, got %d", len(b))
	}
	v := binary.BigEndian.Uint32(b)
	if v != 0x12345678 {
		t.Errorf("expected 0x12345678, got 0x%08X", v)
	}
}

func TestMarshalUint64(t *testing.T) {
	b := marshalUint64(0x0102030405060708)
	if len(b) != 8 {
		t.Fatalf("expected 8 bytes, got %d", len(b))
	}
	v := binary.BigEndian.Uint64(b)
	if v != 0x0102030405060708 {
		t.Errorf("expected 0x0102030405060708, got 0x%016X", v)
	}
}

func TestMarshalString(t *testing.T) {
	b := marshalString("test")
	if len(b) != 8 {
		t.Fatalf("expected 8 bytes, got %d", len(b))
	}
	s, rest := readString(b)
	if s != "test" {
		t.Errorf("expected %q, got %q", "test", s)
	}
	if len(rest) != 0 {
		t.Errorf("expected empty rest, got %d bytes", len(rest))
	}

	// Empty string
	b2 := marshalString("")
	if len(b2) != 4 {
		t.Fatalf("expected 4 bytes for empty string, got %d", len(b2))
	}
	s2, _ := readString(b2)
	if s2 != "" {
		t.Errorf("expected empty string, got %q", s2)
	}
}

// =============================================================================
// Default listen address
// =============================================================================

func TestStart_DefaultAddr(t *testing.T) {
	root := t.TempDir()
	cfg := Config{
		Listen:  "", // should default to ":2222"
		HostKey: filepath.Join(t.TempDir(), "host_key"),
		Users: map[string]User{
			"user": {Password: "pass", Root: root},
		},
	}
	s := New(cfg, testLogger())
	// We can't easily test ":2222" binding (may be in use),
	// but we can verify the code path doesn't panic.
	// Attempt to start - if port 2222 is busy, that's expected.
	err := s.Start()
	if err == nil {
		defer s.Stop()
		// Verify it actually started on some port
		if s.listener == nil {
			t.Error("expected listener to be set")
		}
	}
	// If err != nil, port 2222 is in use — that's fine for this test.
}

// =============================================================================
// pipeChannel: adapts net.Conn to ssh.Channel for unit tests
// =============================================================================

type pipeChannel struct {
	net.Conn
}

func newPipeChannel(conn net.Conn) ssh.Channel {
	return &pipeChannel{Conn: conn}
}

func (p *pipeChannel) SendRequest(name string, wantReply bool, payload []byte) (bool, error) {
	return false, nil
}

func (p *pipeChannel) Stderr() io.ReadWriter {
	return nil
}

func (p *pipeChannel) CloseWrite() error {
	return nil
}

// =============================================================================
// Additional coverage tests for error paths
// =============================================================================

func TestReadPacket_ShortPacket(t *testing.T) {
	// A packet with length=2 and type != INIT: type(1) + 1 byte = too short for id
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	go func() {
		buf := make([]byte, 6)
		binary.BigEndian.PutUint32(buf[0:4], 2) // length = 2
		buf[4] = sshFXPStat                     // not INIT
		buf[5] = 0                              // only 1 extra byte, need 4
		client.Write(buf)
	}()

	_, _, _, err := sess.readPacket()
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestSFTP_RealPath_Traversal(t *testing.T) {
	// When safePath returns "", handleRealPath falls back to root.
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// "/../../../etc/shadow" causes safePath to return "" on all platforms
	sftp.sendPacket(sshFXPRealPath, 200, marshalString("/../../../etc/shadow"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPName {
		t.Fatalf("expected SSH_FXP_NAME, got %d", pktType)
	}
	// Should return "/" (the root)
	count := binary.BigEndian.Uint32(payload[0:4])
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
	name, _ := readString(payload[4:])
	if name != "/" {
		t.Errorf("expected %q for traversal fallback, got %q", "/", name)
	}
}

func TestSFTP_RealPath_NonexistentPath(t *testing.T) {
	// handleRealPath: stat on safe path fails, falls back to root stat
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRealPath, 201, marshalString("does/not/exist"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPName {
		t.Fatalf("expected SSH_FXP_NAME, got %d", pktType)
	}
	// Should still return the resolved path (even though it doesn't exist)
	count := binary.BigEndian.Uint32(payload[0:4])
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
	name, _ := readString(payload[4:])
	if name == "" {
		t.Error("expected non-empty path name")
	}
}

func TestSFTP_Read_LargeLength(t *testing.T) {
	// Test that read length > 256KB is clamped
	root := t.TempDir()
	// Create a file larger than 256KB
	bigData := make([]byte, 300*1024) // 300KB
	for i := range bigData {
		bigData[i] = 'A'
	}
	os.WriteFile(filepath.Join(root, "big.dat"), bigData, 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for reading
	openPayload := marshalString("big.dat")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 300, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Request a read of 1MB (> 256KB, should be clamped)
	readPayload := marshalString(handle)
	readPayload = append(readPayload, marshalUint64(0)...)
	readPayload = append(readPayload, marshalUint32(1024*1024)...) // 1MB
	sftp.sendPacket(sshFXPRead, 301, readPayload)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPData {
		t.Fatalf("expected DATA, got %d", pktType)
	}
	dataLen := binary.BigEndian.Uint32(payload[0:4])
	// Should be clamped to 256KB (1<<18)
	if dataLen != 1<<18 {
		t.Errorf("expected clamped read of %d bytes, got %d", 1<<18, dataLen)
	}

	// Close
	sftp.sendPacket(sshFXPClose, 302, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_MkDir_AlreadyExists(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "existing"), 0755)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	mkdirPayload := marshalString("existing")
	mkdirPayload = append(mkdirPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPMkDir, 400, mkdirPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for already existing dir, got %d", statusCode(payload))
	}
}

func TestSFTP_RmDir_NonEmpty(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "notempty"), 0755)
	os.WriteFile(filepath.Join(root, "notempty", "file.txt"), []byte("x"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRmDir, 401, marshalString("notempty"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for non-empty dir, got %d", statusCode(payload))
	}
}

func TestSFTP_Rename_SourceNotFound(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	renamePayload := marshalString("nonexistent.txt")
	renamePayload = append(renamePayload, marshalString("dest.txt")...)
	sftp.sendPacket(sshFXPRename, 402, renamePayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for rename of non-existent, got %d", statusCode(payload))
	}
}

func TestSFTP_Stat_Traversal(t *testing.T) {
	// Use a path that escapes on ALL platforms (including Windows)
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// "/../../../etc/shadow" escapes on all platforms
	sftp.sendPacket(sshFXPStat, 500, marshalString("/../../../etc/shadow"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_OpenDir_Traversal(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// "/../../../" escapes on all platforms
	sftp.sendPacket(sshFXPOpenDir, 501, marshalString("/../../../"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_MkDir_Traversal(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// "/../../../evil" escapes on all platforms
	mkdirPayload := marshalString("/../../../evil")
	mkdirPayload = append(mkdirPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPMkDir, 502, mkdirPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_Rename_Traversal_Both(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Old path with traversal (escapes on all platforms)
	renamePayload := marshalString("/../../../etc/passwd")
	renamePayload = append(renamePayload, marshalString("dest.txt")...)
	sftp.sendPacket(sshFXPRename, 503, renamePayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}

	// New path with traversal
	renamePayload2 := marshalString("file.txt")
	renamePayload2 = append(renamePayload2, marshalString("/../../../escape.txt")...)
	sftp.sendPacket(sshFXPRename, 504, renamePayload2)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED for new path traversal, got %d", statusCode(payload))
	}
}

func TestSFTP_Open_Traversal(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// "/../../../etc/shadow" escapes on all platforms
	openPayload := marshalString("/../../../etc/shadow")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 505, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_Remove_Traversal(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRemove, 506, marshalString("/../../../etc/shadow"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_RmDir_Traversal(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPRmDir, 507, marshalString("/../../../etc"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXPermissionDenied {
		t.Errorf("expected PERMISSION_DENIED, got %d", statusCode(payload))
	}
}

func TestSFTP_OpenDir_NotFound(t *testing.T) {
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	sftp.sendPacket(sshFXPOpenDir, 508, marshalString("nonexistent_dir"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXNoSuchFile {
		t.Errorf("expected NO_SUCH_FILE, got %d", statusCode(payload))
	}
}

func TestSFTP_ReadDir_FileHandle(t *testing.T) {
	// Use a file handle (not a dir) for ReadDir
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open file (not dir)
	openPayload := marshalString("file.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 509, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Try to ReadDir with a file handle
	sftp.sendPacket(sshFXPReadDir, 510, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for readdir on file handle")
	}

	// Close
	sftp.sendPacket(sshFXPClose, 511, marshalString(handle))
	sftp.recvPacket()
}

func TestStart_ListenError(t *testing.T) {
	// Try to start on an address that will fail (invalid address)
	cfg := Config{
		Listen:  "invalid-not-an-address::::",
		HostKey: filepath.Join(t.TempDir(), "host_key"),
		Users: map[string]User{
			"user": {Password: "pass", Root: t.TempDir()},
		},
	}
	s := New(cfg, testLogger())
	err := s.Start()
	if err == nil {
		s.Stop()
		t.Fatal("expected error for invalid listen address")
	}
}

func TestSFTP_NonSessionChannel(t *testing.T) {
	// Test that non-session channels are rejected
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	clientCfg := sshClientConfig("user", "pass")
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()

	// Try to open a non-session channel type
	_, _, err = client.OpenChannel("direct-tcpip", nil)
	if err == nil {
		t.Error("expected error for non-session channel")
	}
}

func TestSFTP_NonSftpSubsystem(t *testing.T) {
	// Test that non-sftp subsystem requests are handled.
	// The server replies true for subsystem type (even non-sftp ones) but
	// doesn't start the SFTP handler - it just continues the request loop.
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	clientCfg := sshClientConfig("user", "pass")
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()

	session, reqs, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	defer session.Close()
	go ssh.DiscardRequests(reqs)

	// Request a non-sftp subsystem. The server's handleSession code replies
	// true when req.Type == "subsystem" (regardless of the subsystem name),
	// but continues without starting SFTP.
	ok, err := session.SendRequest("subsystem", true, append(marshalUint32(4), []byte("exec")...))
	if err != nil {
		t.Fatalf("subsystem request: %v", err)
	}
	// The reply is true because req.Type == "subsystem"
	if !ok {
		t.Error("expected subsystem request to get true reply (it's still a subsystem type)")
	}
}

func TestSFTP_NonSubsystemRequest(t *testing.T) {
	// Test a non-subsystem request type
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	clientCfg := sshClientConfig("user", "pass")
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()

	session, reqs, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	defer session.Close()
	go ssh.DiscardRequests(reqs)

	// Send a non-subsystem request (e.g. "shell")
	ok, err := session.SendRequest("shell", true, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// Should reply false
	if ok {
		t.Error("expected non-subsystem request to be rejected")
	}
}

func TestSFTP_FStat_DirHandle(t *testing.T) {
	// FStat on a dir handle (no file pointer) should return failure
	root := t.TempDir()
	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open a directory handle
	sftp.sendPacket(sshFXPOpenDir, 600, marshalString("."))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// FStat on dir handle (file is nil since it's a dir open handle)
	sftp.sendPacket(sshFXPFStat, 601, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		t.Fatalf("expected status, got %d", pktType)
	}
	if statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for fstat on dir handle, got %d", statusCode(payload))
	}

	// Close
	sftp.sendPacket(sshFXPClose, 602, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_Write_ReadOnlyFD(t *testing.T) {
	// Open a file for reading only, then try to write to it.
	// The server opens the file with O_RDONLY if only FXF_READ is set.
	// If a write packet targets that handle, WriteAt will fail.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "readonly.txt"), []byte("original"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open for reading only
	openPayload := marshalString("readonly.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 700, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Try to write using the read-only handle
	content := []byte("override")
	writePayload := marshalString(handle)
	writePayload = append(writePayload, marshalUint64(0)...)
	writePayload = append(writePayload, marshalUint32(uint32(len(content)))...)
	writePayload = append(writePayload, content...)
	sftp.sendPacket(sshFXPWrite, 701, writePayload)

	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus || statusCode(payload) != sshFXFailure {
		t.Errorf("expected FAILURE for write on read-only fd, got type=%d code=%d", pktType, statusCode(payload))
	}

	// Close
	sftp.sendPacket(sshFXPClose, 702, marshalString(handle))
	sftp.recvPacket()
}

func TestReadPacket_MidReadEOF(t *testing.T) {
	// Send a valid length header but close the connection before
	// all bytes of the payload arrive.
	server, client := net.Pipe()
	defer server.Close()

	sess := &sftpSession{
		ch:      newPipeChannel(server),
		handles: make(map[string]*openHandle),
	}

	go func() {
		// Claim 100 bytes of payload
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], 100)
		client.Write(buf[:])
		// Write only 5 bytes then close
		client.Write([]byte{sshFXPStat, 0, 0, 0, 1})
		client.Close()
	}()

	_, _, _, err := sess.readPacket()
	if err == nil {
		t.Error("expected error for mid-read EOF")
	}
}

func TestSFTP_FStat_DeletedFile(t *testing.T) {
	// Open a file, delete it, then FStat. On some OSes this still works
	// (Unix keeps the inode), on Windows it may fail.
	root := t.TempDir()
	fpath := filepath.Join(root, "delete_me.txt")
	os.WriteFile(fpath, []byte("hello"), 0644)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open file
	openPayload := marshalString("delete_me.txt")
	openPayload = append(openPayload, marshalUint32(sshFXFRead)...)
	openPayload = append(openPayload, marshalUint32(0)...)
	sftp.sendPacket(sshFXPOpen, 710, openPayload)

	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Delete the underlying file (on Windows this may fail while file is open)
	os.Remove(fpath)

	// FStat - on Windows the file handle still works, on Unix the inode persists
	sftp.sendPacket(sshFXPFStat, 711, marshalString(handle))
	pktType, _, _, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	// Either ATTRS (Unix - inode still valid) or FAILURE (Windows - file deleted)
	if pktType != sshFXPAttrs && pktType != sshFXPStatus {
		t.Fatalf("expected ATTRS or STATUS, got %d", pktType)
	}

	// Close
	sftp.sendPacket(sshFXPClose, 712, marshalString(handle))
	sftp.recvPacket()
}

func TestSFTP_ReadDir_DeletedDir(t *testing.T) {
	// Open a directory handle, delete the directory, then ReadDir.
	root := t.TempDir()
	dirPath := filepath.Join(root, "deleteme")
	os.Mkdir(dirPath, 0755)

	s, addr := startTestServer(t, map[string]User{
		"user": {Password: "pass", Root: root},
	})
	defer s.Stop()

	client, sftp := openSFTPSession(t, addr, "user", "pass")
	defer client.Close()
	defer sftp.ch.Close()

	sftp.sendInit()
	sftp.recvPacket()

	// Open directory
	sftp.sendPacket(sshFXPOpenDir, 720, marshalString("deleteme"))
	pktType, _, payload, err := sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPHandle {
		t.Fatalf("expected handle, got %d", pktType)
	}
	handle, _ := readString(payload)

	// Delete the directory
	os.Remove(dirPath)

	// ReadDir
	sftp.sendPacket(sshFXPReadDir, 721, marshalString(handle))
	pktType, _, payload, err = sftp.recvPacket()
	if err != nil {
		t.Fatalf("recvPacket: %v", err)
	}
	if pktType != sshFXPStatus {
		// On some OSes this may still succeed (return empty list), that's fine
		t.Logf("ReadDir on deleted dir returned type %d (not necessarily an error)", pktType)
	} else {
		code := statusCode(payload)
		if code != sshFXFailure {
			t.Logf("ReadDir returned code %d", code)
		}
	}

	// Close
	sftp.sendPacket(sshFXPClose, 722, marshalString(handle))
	sftp.recvPacket()
}

func TestHostKey_DefaultPath(t *testing.T) {
	// Test the default host key path when config is empty
	cfg := Config{
		Listen:  ":0",
		HostKey: "", // empty = default path /etc/uwas/sftp_host_key
	}
	s := New(cfg, testLogger())
	// On most test machines, /etc/uwas/sftp_host_key won't exist,
	// so it will generate a new key.
	signer, err := s.loadOrGenerateHostKey()
	if err != nil {
		t.Fatalf("loadOrGenerateHostKey: %v", err)
	}
	if signer == nil {
		t.Fatal("expected non-nil signer")
	}
}
