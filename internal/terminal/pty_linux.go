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
		for {
			n, err := master.Read(buf)
			if err != nil {
				return
			}
			data := buf[:n]
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
