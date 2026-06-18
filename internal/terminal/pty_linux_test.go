//go:build linux

package terminal

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/uwaserver/uwas/internal/logger"
)

// --- sanitizeUTF8 ---

func TestSanitizeUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"empty", []byte{}, []byte{}},
		{"ascii", []byte("hello world"), []byte("hello world")},
		{"multibyte_valid", []byte("héllo→世界"), []byte("héllo→世界")},
		{"single_invalid", []byte{0xff}, []byte("?")},
		{"invalid_in_middle", []byte{'a', 0xff, 'b'}, []byte("a?b")},
		{"truncated_multibyte", []byte{'a', 0xe4, 0xb8}, []byte("a??")}, // truncated 世 (3-byte) → two bad bytes
		{"all_invalid", []byte{0x80, 0x81, 0xfe}, []byte("???")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeUTF8(tt.in)
			if string(got) != string(tt.want) {
				t.Errorf("sanitizeUTF8(%v) = %q, want %q", tt.in, got, tt.want)
			}
			// Output must always be valid UTF-8.
			if !utf8.Valid(got) {
				t.Errorf("sanitizeUTF8 output is not valid UTF-8: %v", got)
			}
		})
	}
}

// --- resizeMsg JSON parsing ---

func TestResizeMsgJSON(t *testing.T) {
	var msg resizeMsg
	if err := json.Unmarshal([]byte(`{"type":"resize","cols":120,"rows":40}`), &msg); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if msg.Type != "resize" || msg.Cols != 120 || msg.Rows != 40 {
		t.Fatalf("unexpected parse: %+v", msg)
	}

	// Non-resize type parses but should be ignored by the bridge logic.
	msg = resizeMsg{}
	if err := json.Unmarshal([]byte(`{"type":"other"}`), &msg); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if msg.Type != "other" {
		t.Fatalf("unexpected type: %q", msg.Type)
	}

	// Malformed JSON must error (mirrors the json.Unmarshal == nil guard).
	if err := json.Unmarshal([]byte(`{not json}`), &resizeMsg{}); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// --- defaultShell env logic ---

func TestDefaultShellFromEnv(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	if got := defaultShell(); got != "/usr/bin/zsh" {
		t.Fatalf("defaultShell() = %q, want /usr/bin/zsh", got)
	}
}

func TestDefaultShellFallback(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := defaultShell(); got != "/bin/bash" {
		t.Fatalf("defaultShell() = %q, want /bin/bash", got)
	}
}

// --- openPTY ---

func TestOpenPTYSuccess(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY failed: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	if master == nil || slave == nil {
		t.Fatal("openPTY returned nil file(s)")
	}
	// The slave path should look like /dev/pts/N and be openable (already open).
	if !strings.HasPrefix(slave.Name(), "/dev/pts/") {
		t.Errorf("unexpected slave name: %q", slave.Name())
	}

	// Data written to the master must be readable on the slave.
	if _, err := master.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write to master: %v", err)
	}
	buf := make([]byte, 64)
	_ = slave.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := slave.Read(buf)
	if err != nil {
		t.Fatalf("read from slave: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "ping") {
		t.Errorf("slave did not echo master data: %q", buf[:n])
	}
}

func TestOpenPTYMultiple(t *testing.T) {
	// Allocating several PTYs in a row should always succeed and not leak fds.
	// (Slave numbers may be reused after Close, so we don't assert uniqueness.)
	for i := 0; i < 5; i++ {
		m, s, err := openPTY()
		if err != nil {
			t.Fatalf("openPTY #%d failed: %v", i, err)
		}
		if !strings.HasPrefix(s.Name(), "/dev/pts/") {
			t.Errorf("unexpected slave name: %q", s.Name())
		}
		s.Close()
		m.Close()
	}
}

func TestOpenPTYConcurrent(t *testing.T) {
	// Hold several PTYs open simultaneously to confirm distinct allocations and
	// that the kernel hands out separate slave devices under contention.
	const n = 4
	masters := make([]*os.File, 0, n)
	slaves := make([]*os.File, 0, n)
	seen := map[string]bool{}
	defer func() {
		for _, f := range slaves {
			f.Close()
		}
		for _, f := range masters {
			f.Close()
		}
	}()
	for i := 0; i < n; i++ {
		m, s, err := openPTY()
		if err != nil {
			t.Fatalf("openPTY #%d failed: %v", i, err)
		}
		masters = append(masters, m)
		slaves = append(slaves, s)
		if seen[s.Name()] {
			t.Errorf("duplicate slave while held open: %q", s.Name())
		}
		seen[s.Name()] = true
	}
}

// --- setWinSize ---

func TestSetWinSize(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY failed: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	// setWinSize is best-effort (no return value); exercise it and then verify
	// the size took effect via TIOCGWINSZ on the slave.
	setWinSize(master, 132, 50)

	cols, rows, err := getWinSize(slave)
	if err != nil {
		t.Fatalf("getWinSize: %v", err)
	}
	if cols != 132 || rows != 50 {
		t.Errorf("winsize = %dx%d, want 132x50", cols, rows)
	}
}

func TestSetWinSizeOnClosedFile(t *testing.T) {
	// setWinSize swallows ioctl errors (best-effort). Calling on a closed fd
	// must not panic.
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY failed: %v", err)
	}
	slave.Close()
	master.Close()
	setWinSize(master, 80, 24) // closed fd; must not panic
}

// --- ServeHTTP end-to-end PTY <-> WebSocket bridge ---

// wsTestClient drives a real WebSocket connection against ServeHTTP running on
// a real TCP listener. Client frames are masked per RFC 6455.
type wsTestClient struct {
	conn net.Conn
	br   *bufio.Reader
}

func dialTerminal(t *testing.T, h *Handler) (*wsTestClient, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h}
	served := make(chan struct{})
	go func() { _ = srv.Serve(ln); close(served) }()

	addr := ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		ln.Close()
		t.Fatalf("dial: %v", err)
	}

	req := "GET /terminal HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Origin: http://" + addr + "\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		ln.Close()
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	// Read the 101 status line + headers until the blank line.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	statusLine, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		ln.Close()
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		ln.Close()
		t.Fatalf("unexpected handshake status: %q", statusLine)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			ln.Close()
			t.Fatalf("read headers: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	cli := &wsTestClient{conn: conn, br: br}
	cleanup := func() {
		conn.Close()
		srv.Close()
		ln.Close()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
		}
	}
	return cli, cleanup
}

// writeFrame sends a masked client text frame.
func (c *wsTestClient) writeFrame(data []byte) error {
	var frame []byte
	frame = append(frame, 0x81) // FIN + text
	l := len(data)
	switch {
	case l < 126:
		frame = append(frame, byte(0x80|l))
	case l < 65536:
		frame = append(frame, 0x80|126, byte(l>>8), byte(l))
	default:
		frame = append(frame, 0x80|127, 0, 0, 0, 0, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	frame = append(frame, mask[:]...)
	masked := make([]byte, l)
	for i := range data {
		masked[i] = data[i] ^ mask[i%4]
	}
	frame = append(frame, masked...)
	_, err := c.conn.Write(frame)
	return err
}

// readFrame reads a single server frame (server frames are unmasked).
func (c *wsTestClient) readFrame() (opcode byte, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(c.br, hdr); err != nil {
		return 0, nil, err
	}
	opcode = hdr[0] & 0x0F
	plen := int(hdr[1] & 0x7F)
	switch plen {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(c.br, ext); err != nil {
			return 0, nil, err
		}
		plen = int(ext[0])<<8 | int(ext[1])
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(c.br, ext); err != nil {
			return 0, nil, err
		}
		plen = int(ext[6])<<8 | int(ext[7])
	}
	payload = make([]byte, plen)
	_, err = io.ReadFull(c.br, payload)
	return opcode, payload, err
}

// readTextUntil reads frames until the accumulated text contains want or timeout.
func (c *wsTestClient) readTextUntil(t *testing.T, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		_ = c.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		op, payload, err := c.readFrame()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}
		if op == 0x8 { // close
			break
		}
		acc.Write(payload)
		if strings.Contains(acc.String(), want) {
			return acc.String()
		}
	}
	return acc.String()
}

func TestServeHTTPBridgeEcho(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY bridge only on Linux")
	}
	before := runtime.NumGoroutine()

	// /bin/cat echoes stdin back to stdout via the PTY, exercising both pumps.
	// A non-nil logger also exercises the `h.Logger != nil` Info/Error guards.
	h := &Handler{Shell: "/bin/cat", Logger: logger.New("error", "text")}
	cli, cleanup := dialTerminal(t, h)
	defer cleanup()

	if err := cli.writeFrame([]byte("marco-polo\n")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := cli.readTextUntil(t, "marco-polo", 4*time.Second)
	if !strings.Contains(got, "marco-polo") {
		t.Fatalf("did not receive echo; got %q", got)
	}

	// Send a resize control message — must be consumed (not echoed to PTY).
	if err := cli.writeFrame([]byte(`{"type":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	// Send more data after the resize to confirm the bridge is still alive.
	if err := cli.writeFrame([]byte("after-resize\n")); err != nil {
		t.Fatalf("write after resize: %v", err)
	}
	got = cli.readTextUntil(t, "after-resize", 4*time.Second)
	if !strings.Contains(got, "after-resize") {
		t.Fatalf("bridge dead after resize; got %q", got)
	}
	if strings.Contains(got, `"type":"resize"`) {
		t.Errorf("resize control message was echoed to the shell: %q", got)
	}

	cleanup()
	assertNoGoroutineLeak(t, before)
}

func TestServeHTTPBridgeInvalidUTF8(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY bridge only on Linux")
	}
	// Feed raw invalid UTF-8 bytes through /bin/cat. cat echoes them back over
	// the PTY, so the PTY->WS pump hits the `!utf8.Valid(data)` sanitize branch
	// and replaces the bad bytes with '?'. We frame a recognizable marker around
	// the bad bytes so we can locate them in the (possibly echoed) stream.
	h := &Handler{Shell: "/bin/cat"}
	cli, cleanup := dialTerminal(t, h)
	defer cleanup()

	payload := append([]byte("AB"), 0xff, 0xfe, 0xfd)
	payload = append(payload, []byte("CD\n")...)
	if err := cli.writeFrame(payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := cli.readTextUntil(t, "CD", 4*time.Second)
	// All output frames must be valid UTF-8 (the whole point of sanitizeUTF8).
	if !utf8.ValidString(got) {
		t.Errorf("server emitted invalid UTF-8: %q", got)
	}
	// The raw invalid bytes must not appear verbatim in the output.
	if strings.ContainsRune(got, 0xfffd) {
		// replacement rune is fine
	}
	if strings.Contains(got, "\xff") || strings.Contains(got, "\xfe") || strings.Contains(got, "\xfd") {
		t.Errorf("invalid bytes leaked through unsanitized: %q", got)
	}
}

func TestServeHTTPShellExits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY bridge only on Linux")
	}
	before := runtime.NumGoroutine()

	// /bin/echo writes once then exits on its own. This exercises the
	// cmd.Wait() -> master.Close()/conn.Close() -> wg.Wait() teardown path
	// where the shell exits without the client closing first.
	h := &Handler{Shell: "/bin/echo"}
	cli, cleanup := dialTerminal(t, h)
	defer cleanup()

	// echo with no args just prints a newline then exits; the connection
	// should be closed by the server shortly after.
	deadline := time.Now().Add(5 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		_ = cli.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		op, _, err := cli.readFrame()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			closed = true // EOF/conn reset == server tore down the session
			break
		}
		if op == 0x8 {
			closed = true
			break
		}
	}
	if !closed {
		t.Error("server did not close session after shell exit")
	}

	cleanup()
	assertNoGoroutineLeak(t, before)
}

func TestServeHTTPUpgradeFailure(t *testing.T) {
	// A plain (non-websocket) request must yield a 400 and never open a PTY.
	h := &Handler{Shell: "/bin/cat"}
	srv := httptestServer(t, h)
	defer srv.close()

	resp, err := http.Get(srv.url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServeHTTPShellStartFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY bridge only on Linux")
	}
	// A nonexistent shell makes cmd.Start fail after the PTY is opened, so the
	// server writes an "Error:" frame and returns.
	h := &Handler{Shell: "/nonexistent/shell/binary-xyz"}
	cli, cleanup := dialTerminal(t, h)
	defer cleanup()

	got := cli.readTextUntil(t, "Error:", 4*time.Second)
	if !strings.Contains(got, "Error:") {
		t.Fatalf("expected error frame, got %q", got)
	}
}

// --- helpers ---

// getWinSize reads the terminal window size from a PTY fd via TIOCGWINSZ.
func getWinSize(f *os.File) (cols, rows int, err error) {
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0, errno
	}
	return int(ws.Col), int(ws.Row), nil
}

type httptestSrv struct {
	url   string
	close func()
}

func httptestServer(t *testing.T, h *Handler) *httptestSrv {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h}
	done := make(chan struct{})
	go func() { _ = srv.Serve(ln); close(done) }()
	return &httptestSrv{
		url: "http://" + ln.Addr().String() + "/terminal",
		close: func() {
			srv.Close()
			ln.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		},
	}
}

func assertNoGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	// Allow goroutines to wind down.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Not fatal: the test http.Server keeps a couple of transient goroutines.
	if got := runtime.NumGoroutine(); got > before+3 {
		t.Errorf("possible goroutine leak: before=%d after=%d", before, got)
	}
}
