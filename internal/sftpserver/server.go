// Package sftpserver provides a built-in SFTP server for UWAS.
// Uses x/crypto/ssh for the SSH transport and implements the SFTP protocol
// (SSH_FXP_* packets) directly — no external sftp-server binary needed.
package sftpserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

// Config holds SFTP server configuration.
type Config struct {
	Listen  string          // e.g. ":2222"
	HostKey string          // path to host key, auto-generated if empty
	Users   map[string]User // username → user config

	// Connection limits; zero selects the default (see limits.go).
	MaxHandshakes          int // connections still in the SSH handshake, all sources
	MaxHandshakesPerSource int // the same, per source (IPv6 per /64)
	MaxSessionsPerConn     int // session channels one connection may hold open
}

// User represents an SFTP user with chroot jail.
type User struct {
	Password string // bcrypt hash
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
	wg       sync.WaitGroup
	// pending counts connection goroutines registered with wg whose Done has
	// not fired. Test-only: a WaitGroup counter cannot be read from outside,
	// so this makes the Add ordering externally observable.
	pending atomic.Int64

	// hs bounds connections that are still in the SSH handshake (F15200).
	hs handshakeGate
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
	noteHashCosts(s.users)
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
	noteHashCosts(s.users)
}

// Start begins listening for SFTP connections.
func (s *Server) Start() error {
	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			s.mu.RLock()
			user, ok := s.users[c.User()]
			s.mu.RUnlock()
			if !ok {
				burnPasswordCompare(pass)
				return nil, fmt.Errorf("unknown user")
			}
			if err := comparePassword(user.Password, pass); err != nil {
				return nil, fmt.Errorf("invalid password")
			}
			return &ssh.Permissions{
				Extensions: map[string]string{
					"root":      user.Root,
					"read_only": fmt.Sprintf("%v", user.ReadOnly),
					"pw_hash":   user.Password,
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

	// Build the dummy hash the unknown-user path compares against now, so the
	// first probe does not pay for generating it (F11001).
	go dummyHashFor(int(burnCost.Load()))

	go s.acceptLoop()
	return nil
}

// Stop closes the listener.
func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
}

// Shutdown waits for all active connections to finish before returning.
func (s *Server) Shutdown() {
	s.Stop()
	s.wg.Wait()
}

// acceptSeam is a test-only pause point between Accept returning a connection
// and that connection being handed to handleConn. Registration happens before
// the blocking Accept, so parking here is observable: the counter is already
// non-zero, where the old Add-after-Accept order left it at zero. Production
// installs a no-op; tests substitute a park point.
var acceptSeam = func(net.Conn) {}

// handshakeTimeout bounds the SSH handshake and authentication of a new
// connection (OpenSSH's LoginGraceTime). A variable so tests can shorten it.
var handshakeTimeout = 30 * time.Second

// pendingCount reports connection goroutines registered with wg whose Done
// has not fired. Test-only: a WaitGroup counter cannot be read from outside,
// so this makes the Add ordering externally observable.
func (s *Server) pendingCount() int { return int(s.pending.Load()) }

func (s *Server) acceptLoop() {
	for {
		// Pre-register the connection goroutine BEFORE the blocking Accept.
		// Adding only after Accept returned left a window where a connection
		// accepted just before Stop() closed the listener reached Add(1) once
		// Shutdown's Wait had already started on a zero counter, panicking
		// with "sync: WaitGroup misuse: Add called concurrently with Wait".
		// Registering first keeps the counter non-zero for the whole accept,
		// so Wait cannot return before this goroutine's Done.
		s.wg.Add(1)
		s.pending.Add(1)
		conn, err := s.listener.Accept()
		if err != nil {
			s.wg.Done()
			s.pending.Add(-1)
			if errors.Is(err, net.ErrClosed) {
				return // listener closed gracefully
			}
			// Transient error — brief backoff before retry
			time.Sleep(100 * time.Millisecond)
			continue
		}
		acceptSeam(conn)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(nConn net.Conn) {
	defer func() {
		nConn.Close()
		s.wg.Done()
		s.pending.Add(-1)
	}()

	// Bound the unauthenticated handshake: x/crypto/ssh sets no deadline of
	// its own, so a client that connects and never speaks would otherwise
	// pin this goroutine and its file descriptor forever.
	//
	// The number of connections in this phase is bounded too: an attacker who
	// opens connections and idles would otherwise hold a goroutine and a file
	// descriptor of the web server's own process for each (F15200). A
	// connection over the limit is closed before it says a word.
	release, admitted := s.hs.enter(nConn.RemoteAddr(),
		orDefault(s.config.MaxHandshakes, defaultMaxHandshakes),
		orDefault(s.config.MaxHandshakesPerSource, defaultMaxHandshakesPerSource))
	if !admitted {
		return
	}
	defer release()
	_ = nConn.SetDeadline(time.Now().Add(handshakeTimeout))
	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, s.sshCfg)
	release() // handshake over, successful or not: the slot is free again
	if err != nil {
		return
	}
	defer sshConn.Close()
	_ = nConn.SetDeadline(time.Time{})

	s.logger.Info("SFTP login", "user", sshConn.User(), "remote", nConn.RemoteAddr())

	// Discard global requests
	go ssh.DiscardRequests(reqs)

	// Session channels this connection holds open (F15201): each accepted one
	// starts a goroutine, and x/crypto/ssh imposes no limit of its own.
	var sessions atomic.Int32
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		if int(sessions.Add(1)) > s.maxSessionsPerConn() {
			sessions.Add(-1)
			newCh.Reject(ssh.ResourceShortage, "too many sessions")
			continue
		}

		ch, requests, err := newCh.Accept()
		if err != nil {
			sessions.Add(-1)
			continue
		}

		go func() {
			defer sessions.Add(-1)
			s.handleSession(ch, requests, sshConn.Permissions, sshConn.User())
		}()
	}
}

func (s *Server) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, perms *ssh.Permissions, sshUser string) {
	defer ch.Close()
	// A panic in the SFTP protocol handlers would otherwise propagate up this
	// goroutine and kill the whole process; contain it to this session.
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("SFTP session panic", "panic", r)
		}
	}()

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
		hash := perms.Extensions["pw_hash"]
		s.serveSFTPChecked(ch, root, readOnly, func() bool {
			return s.sessionStillValid(sshUser, root, readOnly, hash)
		})
		return
	}
}

// sessionStillValid reports whether the account a session authenticated as
// still exists with the same root, access mode and password hash. Permissions
// are captured at login, so without this a removed, re-rooted or re-passworded
// user kept full access for as long as the connection stayed open.
func (s *Server) sessionStillValid(user, root string, readOnly bool, hash string) bool {
	s.mu.RLock()
	u, ok := s.users[user]
	s.mu.RUnlock()
	return ok && u.Root == root && u.ReadOnly == readOnly && u.Password == hash
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
	sshFXPFSetStat = 10
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
	sshFXOpUnsupported    = 8
)

// ATTRS flags (SFTP v3)
const (
	sshFileXferAttrSize        = 0x00000001
	sshFileXferAttrUIDGID      = 0x00000002
	sshFileXferAttrPermissions = 0x00000004
	sshFileXferAttrACModTime   = 0x00000008
)

// readDirBatch caps the entries per SSH_FXP_NAME reply. Sending a whole
// directory at once exceeds client message limits (OpenSSH aborts above
// 256 KiB) for large upload or cache folders; OpenSSH's own server uses 100.
const readDirBatch = 100

// maxSessionHandles caps open file and directory handles per session (OpenSSH
// allows 100). Each file handle holds a descriptor from a process-wide pool,
// so one account could otherwise exhaust it for every site.
const maxSessionHandles = 256

// pflags
const (
	sshFXFRead      = 0x00000001
	sshFXFWrite     = 0x00000002
	sshFXFAppend    = 0x00000004
	sshFXFCreat     = 0x00000008
	sshFXFTrunc     = 0x00000010
	sshFXFExclusive = 0x00000020 // SSH_FXF_EXCL: atomic exclusive create — fail if file exists
)

type sftpSession struct {
	ch       ssh.Channel
	root     string
	readOnly bool
	handles  map[string]*openHandle
	nextID   uint32
	rh       *os.Root // chroot handle, opened on first use (see contained)
}

type openHandle struct {
	path    string
	file    *os.File
	dir     *os.File // open directory of an OPENDIR handle
	isDir   bool
	read    bool          // directory already loaded (for readdir)
	entries []os.DirEntry // readdir entries not yet sent
	append  bool          // opened with SSH_FXF_APPEND: writes go to EOF, offset ignored
}

func (s *Server) serveSFTP(ch ssh.Channel, root string, readOnly bool) {
	s.serveSFTPChecked(ch, root, readOnly, nil)
}

// serveSFTPChecked serves a session and, when valid is non-nil, ends it before
// handling any packet once valid reports false.
func (s *Server) serveSFTPChecked(ch ssh.Channel, root string, readOnly bool, valid func() bool) {
	sess := &sftpSession{
		ch:       ch,
		root:     root,
		readOnly: readOnly,
		handles:  make(map[string]*openHandle),
	}
	// A client that disconnects without SSH_FXP_CLOSE would otherwise leave
	// every open file descriptor behind for the life of the process.
	defer func() {
		for k, h := range sess.handles {
			if h.file != nil {
				h.file.Close()
			}
			if h.dir != nil {
				h.dir.Close()
			}
			delete(sess.handles, k)
		}
		if sess.rh != nil {
			sess.rh.Close()
		}
	}()

	for {
		pktType, id, payload, err := sess.readPacket()
		if err != nil {
			return
		}
		if valid != nil && !valid() {
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
			sess.handleSetStat(id, payload)
		case sshFXPFSetStat:
			sess.handleFSetStat(id, payload)
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

	// Reject any ".." path component BEFORE cleaning (prevents traversal).
	// filepath.Clean would normalize "../../etc/shadow" to "/etc/shadow"
	// which on Linux becomes an absolute path outside root. Names that only
	// contain dots, like "a..b.txt" or "...", are ordinary files.
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return ""
		}
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

// contained returns the chroot as an os.Root and p (a path safePath returned)
// relative to it. Every filesystem operation goes through that handle: the
// kernel resolves each component beneath the root, so a directory a tenant
// swaps for a symlink after safePath's check cannot redirect the operation
// outside the chroot (F7110-F7112). The leaf is left as it is (so an entry that
// is itself a symlink can be removed or renamed); see containedFollow.
func (sess *sftpSession) contained(p string) (*os.Root, string, error) {
	return sess.containedAt(p, false)
}

// containedFollow is contained for operations that act on what the path points
// to (STAT, OPENDIR, REALPATH): a leaf symlink is resolved too.
func (sess *sftpSession) containedFollow(p string) (*os.Root, string, error) {
	return sess.containedAt(p, true)
}

// afterResolve runs between the symlink resolution and the os.Root operation; a
// test swaps a directory for a symlink here to check that os.Root still confines
// the resolved path.
var afterResolve = func() {}

func (sess *sftpSession) containedAt(p string, followLeaf bool) (*os.Root, string, error) {
	absRoot, err := filepath.Abs(sess.root)
	if err != nil {
		return nil, "", err
	}
	rel, err := relInRoot(absRoot, p, followLeaf)
	if err != nil {
		return nil, "", err
	}
	afterResolve()
	if sess.rh == nil {
		rh, err := os.OpenRoot(sess.root)
		if err != nil {
			return nil, "", err
		}
		sess.rh = rh
	}
	return sess.rh, rel, nil
}

// relInRoot expresses p relative to the chroot with the symlinks of its
// directory part (and of the leaf when followLeaf) resolved. os.Root refuses a
// symlink with an absolute target even when it points inside the chroot (a
// deploy's "current -> /home/u/releases/N"), which the web server and safePath
// both accept, so those are resolved here; os.Root then still confines the
// resolved path if a component is swapped for a symlink after this check
// (F7401).
func relInRoot(absRoot, p string, followLeaf bool) (string, error) {
	if p == absRoot {
		return ".", nil
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		realRoot = absRoot
	}
	target := ""
	if followLeaf {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			target = real
		}
	}
	if target == "" {
		dir, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			return "", err
		}
		target = filepath.Join(dir, filepath.Base(p))
	}
	rel, err := filepath.Rel(realRoot, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the chroot")
	}
	return rel, nil
}

// removeEntry unlinks safe (a safePath result) without following it.
func (sess *sftpSession) removeEntry(safe string) error {
	rh, rel, err := sess.contained(safe)
	if err != nil {
		return err
	}
	return rh.Remove(rel)
}

// mkdirEntry creates the directory safe (a safePath result).
func (sess *sftpSession) mkdirEntry(safe string) error {
	rh, rel, err := sess.contained(safe)
	if err != nil {
		return err
	}
	return rh.Mkdir(rel, 0755)
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
	if length == 0 {
		err = fmt.Errorf("packet too short")
		return
	}
	if length > 1<<24 { // 16MB max
		err = fmt.Errorf("packet too large: %d", length)
		return
	}
	// Grow the buffer as bytes arrive instead of allocating the declared
	// length up front: a 4-byte header must not pin 16 MiB per session.
	var body bytes.Buffer
	body.Grow(int(min(length, 32<<10)))
	if _, err = io.CopyN(&body, sess.ch, int64(length)); err != nil {
		return
	}
	buf := body.Bytes()
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
	if uint64(n) > uint64(len(b)-4) {
		return "", nil
	}
	end := 4 + int(n)
	return string(b[4:end]), b[end:]
}

// posixMode converts a Go os.FileMode to the POSIX st_mode SFTP v3 puts in the
// permissions field: os.FileMode keeps the file type in high bits of its own
// (ModeDir is 1<<31), which no client reads as S_IFDIR.
func posixMode(m os.FileMode) uint32 {
	mode := uint32(m.Perm())
	switch {
	case m&os.ModeDir != 0:
		mode |= 0o040000
	case m&os.ModeSymlink != 0:
		mode |= 0o120000
	case m&os.ModeNamedPipe != 0:
		mode |= 0o010000
	case m&os.ModeSocket != 0:
		mode |= 0o140000
	case m&os.ModeDevice != 0 && m&os.ModeCharDevice != 0:
		mode |= 0o020000
	case m&os.ModeDevice != 0:
		mode |= 0o060000
	default:
		mode |= 0o100000
	}
	if m&os.ModeSetuid != 0 {
		mode |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		mode |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		mode |= 0o1000
	}
	return mode
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
	binary.BigEndian.PutUint32(buf[20:24], posixMode(info.Mode()))
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
	// Through the chroot handle like STAT: a path swapped for a symlink after
	// safePath must not report the attributes of a file outside the chroot.
	var info os.FileInfo
	rh, rel, err := sess.containedFollow(safe)
	if err == nil {
		info, err = rh.Stat(rel)
	}
	if err != nil {
		info, err = os.Stat(sess.root)
	}
	if err != nil || info == nil {
		// Both stats failed — encodeName(nil) would panic.
		sess.sendStatus(id, sshFXNoSuchFile, "not found")
		return
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
	rh, rel, err := sess.containedFollow(safe)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, "not found")
		return
	}
	info, err := rh.Stat(rel)
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
	rh, rel, err := sess.containedFollow(safe)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, "not a directory")
		return
	}
	dir, err := rh.Open(rel)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, "not a directory")
		return
	}
	if info, err := dir.Stat(); err != nil || !info.IsDir() {
		dir.Close()
		sess.sendStatus(id, sshFXNoSuchFile, "not a directory")
		return
	}
	if len(sess.handles) >= maxSessionHandles {
		dir.Close()
		sess.sendStatus(id, sshFXFailure, "too many open handles")
		return
	}
	handle := sess.newHandle(&openHandle{path: safe, dir: dir, isDir: true})
	sess.sendHandle(id, handle)
}

func (sess *sftpSession) handleReadDir(id uint32, payload []byte) {
	handle, _ := readString(payload)
	h, ok := sess.handles[handle]
	if !ok || !h.isDir {
		sess.sendStatus(id, sshFXFailure, "invalid handle")
		return
	}
	// A handle on an open directory is read one batch at a time: loading the
	// whole directory made every READDIR cost memory proportional to a
	// tenant-controlled entry count (F14602).
	if h.dir == nil && !h.read {
		entries, err := os.ReadDir(h.path)
		if err != nil {
			sess.sendStatus(id, sshFXFailure, err.Error())
			return
		}
		h.entries = entries
		h.read = true
	}

	var namesBuf []byte
	count := 0
	for count == 0 {
		var batch []os.DirEntry
		if h.dir != nil {
			var err error
			batch, err = h.dir.ReadDir(readDirBatch)
			if err != nil && !errors.Is(err, io.EOF) {
				sess.sendStatus(id, sshFXFailure, err.Error())
				return
			}
		} else {
			batch = h.entries
			if len(batch) > readDirBatch {
				batch = batch[:readDirBatch]
			}
			h.entries = h.entries[len(batch):]
		}
		if len(batch) == 0 {
			break
		}
		for _, e := range batch {
			info, err := e.Info()
			if err != nil {
				continue
			}
			namesBuf = append(namesBuf, encodeName(e.Name(), info)...)
			count++
		}
	}
	if count == 0 {
		sess.sendStatus(id, sshFXEOF, "")
		return
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

	if len(sess.handles) >= maxSessionHandles {
		sess.sendStatus(id, sshFXFailure, "too many open handles")
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
		if pflags&sshFXFExclusive != 0 {
			flags |= os.O_EXCL
		}
	}
	if pflags&sshFXFTrunc != 0 {
		flags |= os.O_TRUNC
	}
	if pflags&sshFXFAppend != 0 {
		flags |= os.O_APPEND
	}

	// Add O_NOFOLLOW (Unix-only; 0 on Windows) so a chrooted SFTP user
	// cannot win a TOCTOU race by swapping the final path component for a
	// symlink after safePath() validated containment. Without this, the
	// kernel happily follows a freshly-planted symlink to /etc/shadow.
	rh, rel, err := sess.contained(safe)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, err.Error())
		return
	}
	f, err := rh.OpenFile(rel, flags|noFollowFlag, 0644)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, err.Error())
		return
	}
	handle := sess.newHandle(&openHandle{path: safe, file: f, append: pflags&sshFXFAppend != 0})
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
		if err != nil && !errors.Is(err, io.EOF) {
			// A failed read (I/O error, directory, write-only handle) is not
			// the end of the file: EOF would hand the client a silently
			// truncated download.
			sess.sendStatus(id, sshFXFailure, err.Error())
			return
		}
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
	var err error
	if h.append {
		// os.File refuses WriteAt on O_APPEND files; SFTP v3 says append
		// handles write at end of file regardless of the offset.
		_, err = h.file.Write(data[:dataLen])
	} else {
		_, err = h.file.WriteAt(data[:dataLen], int64(offset))
	}
	if err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

// setAttrs is the subset of an SFTP v3 ATTRS block that SETSTAT applies.
type setAttrs struct {
	flags        uint32
	size         uint64
	perm         uint32
	atime, mtime uint32
}

func parseSetAttrs(b []byte) (setAttrs, bool) {
	var a setAttrs
	if len(b) < 4 {
		return a, false
	}
	a.flags = binary.BigEndian.Uint32(b[0:4])
	b = b[4:]
	if a.flags&sshFileXferAttrSize != 0 {
		if len(b) < 8 {
			return a, false
		}
		a.size = binary.BigEndian.Uint64(b[0:8])
		b = b[8:]
	}
	if a.flags&sshFileXferAttrUIDGID != 0 {
		if len(b) < 8 {
			return a, false
		}
		b = b[8:]
	}
	if a.flags&sshFileXferAttrPermissions != 0 {
		if len(b) < 4 {
			return a, false
		}
		a.perm = binary.BigEndian.Uint32(b[0:4])
		b = b[4:]
	}
	if a.flags&sshFileXferAttrACModTime != 0 {
		if len(b) < 8 {
			return a, false
		}
		a.atime = binary.BigEndian.Uint32(b[0:4])
		a.mtime = binary.BigEndian.Uint32(b[4:8])
	}
	return a, true
}

// checkSetAttrs answers the request itself when nothing may be applied and
// reports whether the caller should go on to apply a.
func (sess *sftpSession) checkSetAttrs(id uint32, a setAttrs, ok bool) bool {
	if !ok {
		sess.sendStatus(id, sshFXFailure, "bad packet")
		return false
	}
	if a.flags&(sshFileXferAttrSize|sshFileXferAttrUIDGID|sshFileXferAttrPermissions|sshFileXferAttrACModTime) == 0 {
		sess.sendStatus(id, sshFXOK, "")
		return false
	}
	if sess.readOnly {
		sess.sendStatus(id, sshFXPermissionDenied, "read only")
		return false
	}
	if a.flags&sshFileXferAttrUIDGID != 0 {
		sess.sendStatus(id, sshFXOpUnsupported, "changing owner is not supported")
		return false
	}
	return true
}

// applySetAttrs applies size, permissions and times through an open file so
// a symlink swapped in after safePath cannot redirect the change. Only the
// rwx bits are honoured: files are owned by the server user, so a setuid or
// setgid bit set here would hand that user's privileges to any local caller.
func (sess *sftpSession) applySetAttrs(id uint32, f *os.File, a setAttrs) {
	var err error
	if a.flags&sshFileXferAttrSize != 0 {
		err = f.Truncate(int64(a.size))
	}
	if err == nil && a.flags&sshFileXferAttrPermissions != 0 {
		err = f.Chmod(os.FileMode(a.perm & 0o777))
	}
	if err == nil && a.flags&sshFileXferAttrACModTime != 0 {
		err = setFileTimes(f, time.Unix(int64(a.atime), 0), time.Unix(int64(a.mtime), 0))
	}
	if err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

func (sess *sftpSession) handleSetStat(id uint32, payload []byte) {
	path, rest := readString(payload)
	a, ok := parseSetAttrs(rest)
	if !sess.checkSetAttrs(id, a, ok) {
		return
	}
	safe := sess.safePath(path)
	if safe == "" {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	flags := os.O_RDONLY
	if a.flags&sshFileXferAttrSize != 0 {
		flags = os.O_WRONLY
	}
	rh, rel, err := sess.contained(safe)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, err.Error())
		return
	}
	f, err := rh.OpenFile(rel, flags|noFollowFlag, 0)
	if err != nil {
		sess.sendStatus(id, sshFXNoSuchFile, err.Error())
		return
	}
	defer f.Close()
	sess.applySetAttrs(id, f, a)
}

func (sess *sftpSession) handleFSetStat(id uint32, payload []byte) {
	handle, rest := readString(payload)
	h, found := sess.handles[handle]
	if !found {
		sess.sendStatus(id, sshFXFailure, "invalid handle")
		return
	}
	a, ok := parseSetAttrs(rest)
	if !sess.checkSetAttrs(id, a, ok) {
		return
	}
	f := h.file
	if f == nil {
		f = h.dir
	}
	if f == nil {
		// Handle without an open file: reopen by path.
		var err error
		f, err = os.OpenFile(h.path, os.O_RDONLY|noFollowFlag, 0)
		if err != nil {
			sess.sendStatus(id, sshFXNoSuchFile, err.Error())
			return
		}
		defer f.Close()
	}
	sess.applySetAttrs(id, f, a)
}

func (sess *sftpSession) handleClose(id uint32, payload []byte) {
	handle, _ := readString(payload)
	h, ok := sess.handles[handle]
	if ok {
		if h.file != nil {
			h.file.Close()
		}
		if h.dir != nil {
			h.dir.Close()
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
	if safe == "" {
		// A symlink pointing outside the root (e.g. one a PHP script
		// planted) fails safePath, but unlinking touches only the link.
		safe = sess.symlinkEntry(path)
	}
	if safe == "" || safe == sess.root {
		sess.sendStatus(id, sshFXPermissionDenied, "access denied")
		return
	}
	if err := sess.removeEntry(safe); err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

// symlinkEntry returns the path of p when it names a symlink whose parent
// directory is inside the chroot, whatever the link points to; "" otherwise.
// Only for operations that remove the entry itself.
func (sess *sftpSession) symlinkEntry(p string) string {
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return ""
		}
	}
	clean := filepath.Clean("/" + p)
	if clean == "/" {
		return ""
	}
	parent := sess.safePath(filepath.Dir(clean))
	if parent == "" {
		return ""
	}
	full := filepath.Join(parent, filepath.Base(clean))
	if info, err := os.Lstat(full); err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	return full
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
	if err := sess.mkdirEntry(safe); err != nil {
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
	if err := sess.removeEntry(safe); err != nil {
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
	rh, relOld, err := sess.contained(safeOld)
	var relNew string
	if err == nil {
		_, relNew, err = sess.contained(safeNew)
	}
	if err == nil {
		err = rh.Rename(relOld, relNew)
	}
	if err != nil {
		sess.sendStatus(id, sshFXFailure, err.Error())
		return
	}
	sess.sendStatus(id, sshFXOK, "")
}

// --- Host Key ---

// hostKeyBeforePersist is a test seam called after a missing key was detected
// and a new one generated, before the new key is published. Nil in production.
var hostKeyBeforePersist func()

func (s *Server) loadOrGenerateHostKey() (ssh.Signer, error) {
	keyPath := s.config.HostKey
	if keyPath == "" {
		keyPath = "/etc/uwas/sftp_host_key"
	}

	// Try loading existing key. Only a missing file means "no key yet": a key
	// that exists but cannot be read (permissions, I/O error) must not be
	// replaced by a fresh identity, which would overwrite it and make every
	// client see a host-key change (F15500).
	data, err := os.ReadFile(keyPath)
	if err == nil {
		return ssh.ParsePrivateKey(data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		// A directory at the key path holds no identity to lose: fall through
		// to the generate-and-persist path, which then degrades to an
		// ephemeral key exactly as before. Anything else that exists but
		// cannot be read is an identity we must not replace.
		if st, sErr := os.Stat(keyPath); sErr != nil || !st.IsDir() {
			return nil, fmt.Errorf("read host key %s: %w", keyPath, err)
		}
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

	// Persist the key so the host identity is stable across restarts —
	// otherwise every restart presents a new host key, producing SSH
	// host-key-mismatch errors and training users to ignore MITM warnings.
	if der, mErr := x509.MarshalPKCS8PrivateKey(priv); mErr == nil {
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if mkErr := os.MkdirAll(filepath.Dir(keyPath), 0700); mkErr != nil {
			s.logger.Warn("could not create SFTP host key dir; key is ephemeral", "error", mkErr)
		} else {
			if hostKeyBeforePersist != nil {
				hostKeyBeforePersist()
			}
			switch won, wErr := publishHostKey(keyPath, pemBytes); {
			case wErr != nil:
				s.logger.Warn("could not persist SFTP host key; key is ephemeral", "error", wErr)
			case !won:
				// Another start published its key first: adopt that identity
				// instead of serving one that will not survive a restart.
				if existing, rErr := os.ReadFile(keyPath); rErr == nil {
					return ssh.ParsePrivateKey(existing)
				}
				s.logger.Warn("SFTP host key published by another start is unreadable; key is ephemeral")
			default:
				s.logger.Info("generated and persisted SFTP host key", "path", keyPath)
				return signer, nil
			}
		}
	}
	s.logger.Warn("SFTP host key is ephemeral (regenerates on restart)")

	return signer, nil
}

// publishHostKey writes pemBytes to a private temp file next to path, syncs it
// and links it into place without replacing an existing file. The key file is
// therefore either absent or complete (a crash mid-write used to leave a
// truncated key that made every later start fail), and two concurrent first
// starts cannot overwrite each other's identity (F15501). won reports whether
// this call created path.
func publishHostKey(path string, pemBytes []byte) (won bool, err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sftp_host_key.tmp-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(pemBytes); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		// Filesystems without hard links: fall back to an atomic rename,
		// which still never exposes a partial key.
		if rErr := os.Rename(tmpName, path); rErr != nil {
			return false, rErr
		}
	}
	return true, nil
}

// comparePassword checks a password against a bcrypt hash.
// Rejects legacy plaintext passwords.
func comparePassword(stored string, pass []byte) error {
	if strings.HasPrefix(stored, "$2a$") || strings.HasPrefix(stored, "$2b$") || strings.HasPrefix(stored, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(stored), pass)
	}
	burnPasswordCompare(pass)
	return fmt.Errorf("legacy plaintext password rejected")
}

var (
	dummyMu     sync.Mutex
	dummyHashes = map[int][]byte{}

	// burnCost is the bcrypt cost the dummy compare runs at. It follows the
	// strongest hash among the configured users: a dummy cheaper than the real
	// hashes (the old fixed bcrypt.DefaultCost against production cost 14) lets
	// a client tell an unknown username from a known one by response time
	// (F11001).
	burnCost atomic.Int64
)

// noteHashCosts records the bcrypt cost the dummy compare must match.
func noteHashCosts(users map[string]User) {
	cost := 0
	for _, u := range users {
		if c, err := bcrypt.Cost([]byte(u.Password)); err == nil && c > cost {
			cost = c
		}
	}
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	burnCost.Store(int64(cost))
}

// dummyHashFor returns a bcrypt hash at the given cost that no password matches.
func dummyHashFor(cost int) []byte {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	dummyMu.Lock()
	defer dummyMu.Unlock()
	if h, ok := dummyHashes[cost]; ok {
		return h
	}
	h, err := bcrypt.GenerateFromPassword([]byte("uwas-sftp-dummy"), cost)
	if err != nil {
		return nil
	}
	dummyHashes[cost] = h
	return h
}

// burnPasswordCompare runs a bcrypt comparison that always fails, so a
// rejection that never reaches a real hash (unknown user, legacy plaintext
// entry) costs the same as a wrong password and does not reveal which
// usernames exist.
func burnPasswordCompare(pass []byte) {
	_ = bcrypt.CompareHashAndPassword(dummyHashFor(int(burnCost.Load())), pass)
}
