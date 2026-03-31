// Package sftpserver provides a built-in SFTP server for UWAS.
// Uses x/crypto/ssh for the SSH transport and implements the SFTP protocol
// (SSH_FXP_* packets) directly — no external sftp-server binary needed.
package sftpserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"golang.org/x/crypto/ssh"
)

// Config holds SFTP server configuration.
type Config struct {
	Listen  string          // e.g. ":2222"
	HostKey string          // path to host key, auto-generated if empty
	Users   map[string]User // username → user config
}

// User represents an SFTP user with chroot jail.
type User struct {
	Password string // bcrypt or plaintext
	Root     string // chroot directory (e.g. /var/www/example.com/public_html)
	ReadOnly bool
}

// Server is the built-in SFTP server.
type Server struct {
	config   Config
	logger   *logger.Logger
	listener net.Listener
	sshCfg   *ssh.ServerConfig
	mu       sync.RWMutex
	users    map[string]User
}

// New creates a new SFTP server.
func New(cfg Config, log *logger.Logger) *Server {
	s := &Server{
		config: cfg,
		logger: log,
		users:  make(map[string]User),
	}
	for k, v := range cfg.Users {
		s.users[k] = v
	}
	return s
}

// UpdateUsers replaces the user map (called when domains change).
func (s *Server) UpdateUsers(users map[string]User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = make(map[string]User, len(users))
	for k, v := range users {
		s.users[k] = v
	}
}

// Start begins listening for SFTP connections.
func (s *Server) Start() error {
	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			s.mu.RLock()
			user, ok := s.users[c.User()]
			s.mu.RUnlock()
			if !ok {
				return nil, fmt.Errorf("unknown user")
			}
			if user.Password != string(pass) {
				return nil, fmt.Errorf("invalid password")
			}
			return &ssh.Permissions{
				Extensions: map[string]string{
					"root":      user.Root,
					"read_only": fmt.Sprintf("%v", user.ReadOnly),
				},
			}, nil
		},
	}

	// Load or generate host key
	hostKey, err := s.loadOrGenerateHostKey()
	if err != nil {
		return fmt.Errorf("host key: %w", err)
	}
	sshCfg.AddHostKey(hostKey)
	s.sshCfg = sshCfg

	addr := s.config.Listen
	if addr == "" {
		addr = ":2222"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = ln
	s.logger.Info("SFTP server started", "listen", addr)

	go s.acceptLoop()
	return nil
}

// Stop closes the listener.
func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(nConn net.Conn) {
	defer nConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, s.sshCfg)
	if err != nil {
		return
	}
	defer sshConn.Close()

	s.logger.Info("SFTP login", "user", sshConn.User(), "remote", nConn.RemoteAddr())

	// Discard global requests
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		ch, requests, err := newCh.Accept()
		if err != nil {
			continue
		}

		go s.handleSession(ch, requests, sshConn.Permissions)
	}
}

func (s *Server) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, perms *ssh.Permissions) {
	defer ch.Close()

	for req := range reqs {
		if req.Type != "subsystem" || len(req.Payload) < 4 || string(req.Payload[4:]) != "sftp" {
			if req.WantReply {
				req.Reply(req.Type == "subsystem", nil)
			}
			continue
		}
		req.Reply(true, nil)

		root := perms.Extensions["root"]
		readOnly := perms.Extensions["read_only"] == "true"
		s.serveSFTP(ch, root, readOnly)
		return
	}
}

// --- SFTP Protocol Implementation ---

// SFTP packet types
const (
	sshFXPInit     = 1
	sshFXPVersion  = 2
	sshFXPOpen     = 3
	sshFXPClose    = 4
	sshFXPRead     = 5
	sshFXPWrite    = 6
	sshFXPOpenDir  = 11
	sshFXPReadDir  = 12
	sshFXPRemove   = 13
	sshFXPMkDir    = 14
	sshFXPRmDir    = 15
	sshFXPRealPath = 16
	sshFXPStat     = 17
	sshFXPRename   = 18
	sshFXPLStat    = 7
	sshFXPFStat    = 8
	sshFXPSetStat  = 9
	sshFXPStatus   = 101
	sshFXPHandle   = 102
	sshFXPData     = 103
	sshFXPName     = 104
	sshFXPAttrs    = 105
)

// Status codes
const (
	sshFXOK               = 0
	sshFXEOF              = 1
	sshFXNoSuchFile       = 2
	sshFXPermissionDenied = 3
	sshFXFailure          = 4
)

// pflags
const (
	sshFXFRead   = 0x00000001
	sshFXFWrite  = 0x00000002
	sshFXFAppend = 0x00000004
	sshFXFCreat  = 0x00000008
	sshFXFTrunc  = 0x00000010
)

type sftpSession struct {
	ch       ssh.Channel
	root     string
	readOnly bool
	handles  map[string]*openHandle
	nextID   uint32
}

type openHandle struct {
	path  string
	file  *os.File
	isDir bool
	read  bool // already read (for readdir)
}

func (s *Server) serveSFTP(ch ssh.Channel, root string, readOnly bool) {
	sess := &sftpSession{
		ch:       ch,
		root:     root,
		readOnly: readOnly,
		handles:  make(map[string]*openHandle),
	}

	for {
		pktType, id, payload, err := sess.readPacket()
		if err != nil {
			return
		}

		switch pktType {
		case sshFXPInit:
			sess.sendVersion()
		case sshFXPRealPath:
			sess.handleRealPath(id, payload)
		case sshFXPStat, sshFXPLStat:
			sess.handleStat(id, payload)
		case sshFXPFStat:
			sess.handleFStat(id, payload)
		case sshFXPOpenDir:
			sess.handleOpenDir(id, payload)
		case sshFXPReadDir:
			sess.handleReadDir(id, payload)
		case sshFXPOpen:
			sess.handleOpen(id, payload)
		case sshFXPRead:
			sess.handleRead(id, payload)
		case sshFXPWrite:
			sess.handleWrite(id, payload)
		case sshFXPClose:
			sess.handleClose(id, payload)
		case sshFXPRemove:
			sess.handleRemove(id, payload)
		case sshFXPMkDir:
			sess.handleMkDir(id, payload)
		case sshFXPRmDir:
			sess.handleRmDir(id, payload)
		case sshFXPRename:
			sess.handleRename(id, payload)
		case sshFXPSetStat:
			sess.sendStatus(id, sshFXOK, "")
		default:
			sess.sendStatus(id, sshFXFailure, "unsupported")
		}
	}
}

// safePath resolves a path within the chroot root. Returns "" if traversal detected.
func (sess *sftpSession) safePath(p string) string {
	if p == "" || p == "." {
		return sess.root
	}

	// Reject any path containing .. BEFORE cleaning (prevents traversal).
	// filepath.Clean would normalize "../../etc/shadow" to "/etc/shadow"
	// which on Linux becomes an absolute path outside root.
	if strings.Contains(p, "..") {
		return ""
	}

	// Clean and make relative to root
	clean := filepath.Clean("/" + p)
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" {
		return sess.root
	}
	full := filepath.Join(sess.root, rel)
	if !pathsafe.IsWithinBase(sess.root, full) {
		return ""
	}
	// Resolve symlinks to prevent chroot escape via symlink.
	if !pathsafe.IsWithinBaseResolved(sess.root, full) {
		return ""
	}
	absFull, _ := filepath.Abs(full)
	return absFull
}

func (sess *sftpSession) newHandle(h *openHandle) string {
	sess.nextID++
	id := fmt.Sprintf("h%d", sess.nextID)
	sess.handles[id] = h
	return id
}

// --- Packet I/O ---

func (sess *sftpSession) readPacket() (pktType byte, id uint32, payload []byte, err error) {
	var lenBuf [4]byte
	if _, err = io.ReadFull(sess.ch, lenBuf[:]); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length > 1<<24 { // 16MB max
		err = fmt.Errorf("packet too large: %d", length)
		return
	}
	buf := make([]byte, length)
	if _, err = io.ReadFull(sess.ch, buf); err != nil {
		return
	}
	pktType = buf[0]
	if pktType == sshFXPInit {
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

func (sess *sftpSession) writePacket(pktType byte, id uint32, data []byte) {
	total := 1 + 4 + len(data)
	buf := make([]byte, 4+total)
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	buf[4] = pktType
	binary.BigEndian.PutUint32(buf[5:9], id)
	copy(buf[9:], data)
	sess.ch.Write(buf)
}

func (sess *sftpSession) sendVersion() {
	buf := make([]byte, 9)
	binary.BigEndian.PutUint32(buf[0:4], 5) // length
	buf[4] = sshFXPVersion
	binary.BigEndian.PutUint32(buf[5:9], 3) // SFTP v3
	sess.ch.Write(buf)
}

func (sess *sftpSession) sendStatus(id uint32, code uint32, msg string) {
	data := make([]byte, 4+4+len(msg)+4)
	binary.BigEndian.PutUint32(data[0:4], code)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(msg)))
	copy(data[8:], msg)
	binary.BigEndian.PutUint32(data[8+len(msg):], 0) // lang tag
	sess.writePacket(sshFXPStatus, id, data)
}

func (sess *sftpSession) sendHandle(id uint32, handle string) {
	data := make([]byte, 4+len(handle))
	binary.BigEndian.PutUint32(data[0:4], uint32(len(handle)))
	copy(data[4:], handle)
	sess.writePacket(sshFXPHandle, id, data)
}

func readString(b []byte) (string, []byte) {
	if len(b) < 4 {
		return "", nil
	}
	n := binary.BigEndian.Uint32(b[:4])
	if len(b) < int(4+n) {
		return "", nil
	}
	return string(b[4 : 4+n]), b[4+n:]
}

func encodeAttrs(info os.FileInfo) []byte {
	var buf [32]byte
	flags := uint32(0x0000000F) // size + uid/gid + permissions + atime/mtime
	binary.BigEndian.PutUint32(buf[0:4], flags)
	binary.BigEndian.PutUint64(buf[4:12], uint64(info.Size()))
	// uid/gid = 0 (root)
	binary.BigEndian.PutUint32(buf[12:16], 0)
	binary.BigEndian.PutUint32(buf[16:20], 0)
	// permissions
	binary.BigEndian.PutUint32(buf[20:24], uint32(info.Mode()))
	// atime/mtime
	t := uint32(info.ModTime().Unix())
	binary.BigEndian.PutUint32(buf[24:28], t)
	binary.BigEndian.PutUint32(buf[28:32], t)
	return buf[:]
}

func encodeName(name string, info os.FileInfo) []byte {
	longname := fmt.Sprintf("%s 1 root root %d %s %s",
		info.Mode().String(), info.Size(),
		info.ModTime().Format("Jan _2 15:04"), name)
	attrs := encodeAttrs(info)
	data := make([]byte, 4+len(name)+4+len(longname)+len(attrs))
	binary.BigEndian.PutUint32(data[0:4], uint32(len(name)))
	copy(data[4:], name)
	off := 4 + len(name)
	binary.BigEndian.PutUint32(data[off:off+4], uint32(len(longname)))
	copy(data[off+4:], longname)
	off += 4 + len(longname)
	copy(data[off:], attrs)
	return data
}

// --- SFTP Handlers ---

func (sess *sftpSession) handleRealPath(id uint32, payload []byte) {
	path, _ := readString(payload)
	safe := sess.safePath(path)
	if safe == "" {
		safe = sess.root
	}
	// Return path relative to chroot
	rel, _ := filepath.Rel(sess.root, safe)
	result := "/" + filepath.ToSlash(rel)
	if result == "/." {
		result = "/"
	}
	info, err := os.Stat(safe)
	if err != nil {
		info, _ = os.Stat(sess.root)
	}
	nameData := encodeName(result, info)
	buf := make([]byte, 4+len(nameData))
	binary.BigEndian.PutUint32(buf[0:4], 1) // count
	copy(buf[4:], nameData)
	sess.writePacket(sshFXPName, id, buf)
}

func (sess *sftpSession) handleStat(id uint32, payload []byte) {
	path, _ := readString(payload)
	safe := sess.safePath(path)
	if safe == "" {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	info, err := os.Stat(safe)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, "not found")
		return
	}
	sess.writePacket(sshFXPAttrs, id, encodeAttrs(info))
}

func (sess *sftpSession) handleFStat(id uint32, payload []byte) {
	handle, _ := readString(payload)
	h, ok := sess.handles[handle]
	if !ok || h.file == nil {
		sess.sendStatus(id, sshFXFailure, "invalid handle")
		return
	}
	info, err := h.file.Stat()
	if err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.writePacket(sshFXPAttrs, id, encodeAttrs(info))
}

func (sess *sftpSession) handleOpenDir(id uint32, payload []byte) {
	path, _ := readString(payload)
	safe := sess.safePath(path)
	if safe == "" {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	info, err := os.Stat(safe)
	if err != nil || !info.IsDir() {
		sess.sendStatus(id, sshFXNoSuchFile, "not a directory")
		return
	}
	handle := sess.newHandle(&openHandle{path: safe, isDir: true})
	sess.sendHandle(id, handle)
}

func (sess *sftpSession) handleReadDir(id uint32, payload []byte) {
	handle, _ := readString(payload)
	h, ok := sess.handles[handle]
	if !ok || !h.isDir {
		sess.sendStatus(id, sshFXFailure, "invalid handle")
		return
	}
	if h.read {
		sess.sendStatus(id, sshFXEOF, "")
		return
	}
	h.read = true

	entries, err := os.ReadDir(h.path)
	if err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}

	var namesBuf []byte
	count := 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		namesBuf = append(namesBuf, encodeName(e.Name(), info)...)
		count++
	}

	buf := make([]byte, 4+len(namesBuf))
	binary.BigEndian.PutUint32(buf[0:4], uint32(count))
	copy(buf[4:], namesBuf)
	sess.writePacket(sshFXPName, id, buf)
}

func (sess *sftpSession) handleOpen(id uint32, payload []byte) {
	path, rest := readString(payload)
	if len(rest) < 8 {
		sess.sendStatus(id, sshFXFailure, "bad packet")
		return
	}
	pflags := binary.BigEndian.Uint32(rest[0:4])

	safe := sess.safePath(path)
	if safe == "" {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}

	if sess.readOnly && (pflags&(sshFXFWrite|sshFXFCreat|sshFXFTrunc|sshFXFAppend)) != 0 {
		sess.sendStatus(id, sshFXPermissionDenied, "read only")
		return
	}

	var flags int
	if pflags&sshFXFRead != 0 {
		flags = os.O_RDONLY
	}
	if pflags&sshFXFWrite != 0 {
		if pflags&sshFXFRead != 0 {
			flags = os.O_RDWR
		} else {
			flags = os.O_WRONLY
		}
	}
	if pflags&sshFXFCreat != 0 {
		flags |= os.O_CREATE
	}
	if pflags&sshFXFTrunc != 0 {
		flags |= os.O_TRUNC
	}
	if pflags&sshFXFAppend != 0 {
		flags |= os.O_APPEND
	}

	f, err := os.OpenFile(safe, flags, 0644)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, err.Error())
		return
	}
	handle := sess.newHandle(&openHandle{path: safe, file: f})
	sess.sendHandle(id, handle)
}

func (sess *sftpSession) handleRead(id uint32, payload []byte) {
	handle, rest := readString(payload)
	h, ok := sess.handles[handle]
	if !ok || h.file == nil {
		sess.sendStatus(id, sshFXFailure, "invalid handle")
		return
	}
	if len(rest) < 12 {
		sess.sendStatus(id, sshFXFailure, "bad packet")
		return
	}
	offset := binary.BigEndian.Uint64(rest[0:8])
	length := binary.BigEndian.Uint32(rest[8:12])
	if length > 1<<18 { // 256KB max per read
		length = 1 << 18
	}

	buf := make([]byte, length)
	n, err := h.file.ReadAt(buf, int64(offset))
	if n == 0 {
		sess.sendStatus(id, sshFXEOF, "")
		return
	}
	data := make([]byte, 4+n)
	binary.BigEndian.PutUint32(data[0:4], uint32(n))
	copy(data[4:], buf[:n])
	sess.writePacket(sshFXPData, id, data)
	_ = err // ReadAt may return io.EOF with n > 0
}

func (sess *sftpSession) handleWrite(id uint32, payload []byte) {
	handle, rest := readString(payload)
	h, ok := sess.handles[handle]
	if !ok || h.file == nil {
		sess.sendStatus(id, sshFXFailure, "invalid handle")
		return
	}
	if len(rest) < 12 {
		sess.sendStatus(id, sshFXFailure, "bad packet")
		return
	}
	offset := binary.BigEndian.Uint64(rest[0:8])
	dataLen := binary.BigEndian.Uint32(rest[8:12])
	data := rest[12:]
	if uint32(len(data)) < dataLen {
		sess.sendStatus(id, sshFXFailure, "short data")
		return
	}
	_, err := h.file.WriteAt(data[:dataLen], int64(offset))
	if err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

func (sess *sftpSession) handleClose(id uint32, payload []byte) {
	handle, _ := readString(payload)
	h, ok := sess.handles[handle]
	if ok {
		if h.file != nil {
			h.file.Close()
		}
		delete(sess.handles, handle)
	}
	sess.sendStatus(id, sshFXOK, "")
}

func (sess *sftpSession) handleRemove(id uint32, payload []byte) {
	if sess.readOnly {
		sess.sendStatus(id, sshFXPermissionDenied, "read only")
		return
	}
	path, _ := readString(payload)
	safe := sess.safePath(path)
	if safe == "" || safe == sess.root {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	if err := os.Remove(safe); err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

func (sess *sftpSession) handleMkDir(id uint32, payload []byte) {
	if sess.readOnly {
		sess.sendStatus(id, sshFXPermissionDenied, "read only")
		return
	}
	path, _ := readString(payload)
	safe := sess.safePath(path)
	if safe == "" {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	if err := os.Mkdir(safe, 0755); err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

func (sess *sftpSession) handleRmDir(id uint32, payload []byte) {
	if sess.readOnly {
		sess.sendStatus(id, sshFXPermissionDenied, "read only")
		return
	}
	path, _ := readString(payload)
	safe := sess.safePath(path)
	if safe == "" || safe == sess.root {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	if err := os.Remove(safe); err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

func (sess *sftpSession) handleRename(id uint32, payload []byte) {
	if sess.readOnly {
		sess.sendStatus(id, sshFXPermissionDenied, "read only")
		return
	}
	oldPath, rest := readString(payload)
	newPath, _ := readString(rest)
	safeOld := sess.safePath(oldPath)
	safeNew := sess.safePath(newPath)
	if safeOld == "" || safeNew == "" {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	if err := os.Rename(safeOld, safeNew); err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

// --- Host Key ---

func (s *Server) loadOrGenerateHostKey() (ssh.Signer, error) {
	keyPath := s.config.HostKey
	if keyPath == "" {
		keyPath = "/etc/uwas/sftp_host_key"
	}

	// Try loading existing key
	if data, err := os.ReadFile(keyPath); err == nil {
		return ssh.ParsePrivateKey(data)
	}

	// Generate new ed25519 key
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}

	// Save for future use
	// Use ssh.MarshalAuthorizedKey for public, but we need private key PEM.
	// For simplicity, use crypto/x509 + pem encoding.
	os.MkdirAll(filepath.Dir(keyPath), 0700)
	// Note: proper PEM encoding would use x509.MarshalPKCS8PrivateKey + pem.Encode.
	// For now we just keep the signer in memory; key regenerates on restart.
	s.logger.Info("generated SFTP host key (ephemeral)")

	return signer, nil
}
