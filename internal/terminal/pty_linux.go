//go:build linux

package terminal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/bash"
}

type resizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// ServeHTTP handles the WebSocket upgrade and PTY bridge.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.UpgradeWebSocket(w, r)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("ws upgrade failed", "error", err)
		}
		http.Error(w, "websocket upgrade failed", http.StatusBadRequest)
		return
	}
	defer conn.Close()

	master, slave, err := openPTY()
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("pty open failed", "error", err)
		}
		conn.WriteText([]byte("Error: " + err.Error()))
		return
	}
	defer master.Close()

	cmd := exec.Command(h.Shell)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	if err := cmd.Start(); err != nil {
		if h.Logger != nil {
			h.Logger.Error("shell start failed", "error", err)
		}
		conn.WriteText([]byte("Error: " + err.Error()))
		slave.Close()
		return
	}
	slave.Close()

	if h.Logger != nil {
		h.Logger.Info("terminal session started", "pid", cmd.Process.Pid, "shell", h.Shell)
	}

	var wg sync.WaitGroup

	// PTY → WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		// pending carries a multi-byte rune that straddled a read boundary. A
		// PTY is a byte stream with no character boundaries, so the tail of a
		// read can be the start of a rune whose continuation bytes are still
		// in flight. Checking such a partial sequence with utf8.Valid reports
		// it invalid, sanitizeUTF8 then replaces its bytes with '?', and text
		// the shell actually produced is corrupted. Carry it into the next read
		// instead and only sanitize once the rune is whole.
		var pending []byte
		for {
			n, err := master.Read(buf)
			if err != nil {
				return
			}
			data := buf[:n]
			if len(pending) > 0 {
				data = append(pending, data...)
			}
			if cut := incompleteUTF8Len(data); cut > 0 {
				// Hold the partial rune back for the next read. pending[:0:0]
				// forces a fresh backing array so the copy cannot alias data.
				pending = append(pending[:0:0], data[len(data)-cut:]...)
				data = data[:len(data)-cut]
			} else {
				pending = nil
			}
			if len(data) == 0 {
				continue
			}
			if !utf8.Valid(data) {
				data = sanitizeUTF8(data)
			}
			if conn.WriteText(data) != nil {
				return
			}
		}
	}()

	// WebSocket → PTY
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			data, err := conn.ReadMessage()
			if err != nil {
				_ = cmd.Process.Signal(syscall.SIGHUP)
				return
			}
			if len(data) > 0 && data[0] == '{' {
				var msg resizeMsg
				if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
					setWinSize(master, msg.Cols, msg.Rows)
					continue
				}
			}
			master.Write(data)
		}
	}()

	_ = cmd.Wait()
	// Unblock both pumps before waiting: closing master makes the PTY→WS
	// reader's Read return, and closing conn makes the WS→PTY reader's
	// ReadMessage return. Without this, a shell that exits on its own (the user
	// typing `exit`) leaves the WS→PTY goroutine blocked forever on
	// ReadMessage, so wg.Wait() never returns and the deferred closes never run
	// — leaking a goroutine, the PTY master fd and the connection per session.
	master.Close()
	conn.Close()
	if h.Logger != nil {
		h.Logger.Info("terminal session ended", "pid", cmd.Process.Pid)
	}
	wg.Wait()
}

func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	var ptn uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("TIOCGPTN: %v", errno)
	}

	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("TIOCSPTLCK: %v", errno)
	}

	slaveName := fmt.Sprintf("/dev/pts/%d", ptn)
	slave, err = os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("open %s: %w", slaveName, err)
	}
	return master, slave, nil
}

func setWinSize(f *os.File, cols, rows int) {
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}

// incompleteUTF8Len returns the number of trailing bytes at the end of data
// that form the start of a multi-byte rune whose continuation bytes have not
// arrived yet, or 0 when data ends on a character boundary.
//
// A PTY delivers a byte stream, not characters, so a rune can straddle two
// reads. Those trailing bytes are not "invalid UTF-8" — they are a prefix whose
// rest is still in flight — so the caller must carry them into the next read
// rather than let sanitizeUTF8 replace them with '?'.
func incompleteUTF8Len(data []byte) int {
	// A rune is at most utf8.UTFMax bytes, so a truncated one can only be
	// within the last UTFMax-1 bytes.
	max := utf8.UTFMax - 1
	if len(data) < max {
		max = len(data)
	}
	for i := 1; i <= max; i++ {
		b := data[len(data)-i]
		if b < utf8.RuneSelf {
			// ASCII byte: a definite boundary, nothing is pending.
			return 0
		}
		if utf8.RuneStart(b) {
			// b starts a rune. Compare how many bytes it announced with how
			// many we actually have.
			want := 1
			switch {
			case b&0xE0 == 0xC0:
				want = 2
			case b&0xF0 == 0xE0:
				want = 3
			case b&0xF8 == 0xF0:
				want = 4
			}
			if i < want {
				return i
			}
			// The rune is complete (or b is an invalid lead such as 0xFF,
			// which utf8.Valid will reject later) — not a split boundary.
			return 0
		}
		// Continuation byte: keep scanning backwards for its lead byte.
	}
	return 0
}

func sanitizeUTF8(data []byte) []byte {
	result := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			result = append(result, '?')
			data = data[1:]
		} else {
			result = append(result, data[:size]...)
			data = data[size:]
		}
	}
	return result
}

// Ensure Handler satisfies http.Handler.
var _ http.Handler = (*Handler)(nil)
