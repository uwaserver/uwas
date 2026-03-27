// Package terminal provides a WebSocket-to-PTY bridge for browser-based shell access.
package terminal

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"github.com/uwaserver/uwas/internal/logger"
)

// Handler upgrades HTTP to WebSocket and bridges to a PTY shell.
type Handler struct {
	Logger *logger.Logger
	Shell  string
}

// New creates a terminal handler.
func New(log *logger.Logger) *Handler {
	return &Handler{Logger: log, Shell: defaultShell()}
}

// --- Minimal WebSocket implementation (no external dependency) ---

// WSConn wraps a hijacked connection for WebSocket framing.
type WSConn struct {
	rwc io.ReadWriteCloser
}

// UpgradeWebSocket performs the HTTP→WebSocket handshake.
func UpgradeWebSocket(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	if r.Header.Get("Upgrade") != "websocket" {
		return nil, fmt.Errorf("not a websocket request")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("server does not support hijacking")
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	accept := computeAcceptKey(key)

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	bufrw.Write([]byte(resp))
	bufrw.Flush()

	// bufrw wraps conn; we use conn directly for unbuffered I/O after flush
	return &WSConn{rwc: conn}, nil
}

func (c *WSConn) ReadMessage() ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.rwc, header); err != nil {
		return nil, err
	}

	opcode := header[0] & 0x0F
	masked := (header[1] & 0x80) != 0
	payloadLen := int(header[1] & 0x7F)

	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.rwc, ext); err != nil {
			return nil, err
		}
		payloadLen = int(ext[0])<<8 | int(ext[1])
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.rwc, ext); err != nil {
			return nil, err
		}
		payloadLen = int(ext[4])<<24 | int(ext[5])<<16 | int(ext[6])<<8 | int(ext[7])
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.rwc, mask[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(c.rwc, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}

	if opcode == 0x8 { // close frame
		return nil, io.EOF
	}
	return payload, nil
}

func (c *WSConn) WriteText(data []byte) error {
	frame := make([]byte, 0, 10+len(data))
	frame = append(frame, 0x81) // FIN + text opcode
	if len(data) < 126 {
		frame = append(frame, byte(len(data)))
	} else if len(data) < 65536 {
		frame = append(frame, 126, byte(len(data)>>8), byte(len(data)))
	} else {
		frame = append(frame, 127, 0, 0, 0, 0,
			byte(len(data)>>24), byte(len(data)>>16), byte(len(data)>>8), byte(len(data)))
	}
	frame = append(frame, data...)
	_, err := c.rwc.Write(frame)
	return err
}

func (c *WSConn) Close() error {
	c.rwc.Write([]byte{0x88, 0x00}) // close frame
	return c.rwc.Close()
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
