// Package terminal provides a WebSocket-to-PTY bridge for browser-based shell access.
package terminal

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/logger"
)

// Handler upgrades HTTP to WebSocket and bridges to a PTY shell.
type Handler struct {
	Logger        *logger.Logger
	Shell         string
	AllowedOrigin string // If set, only this origin is allowed (e.g., "https://panel.example.com")
}

// New creates a terminal handler.
func New(log *logger.Logger) *Handler {
	return &Handler{Logger: log, Shell: defaultShell()}
}

// CheckOrigin validates the Origin header against the allowed origin.
// If AllowedOrigin is empty, allow but verify host matches (same-origin fallback).
// Returns true if the request origin is allowed.
func (h *Handler) CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Reject connections without Origin header — they bypass origin checking
		// entirely and could be used for cross-site WebSocket hijacking.
		// Non-browser clients that don't send Origin should set AllowedOrigin
		// explicitly; without it we have no trust anchor and must reject.
		return false
	}

	// If AllowedOrigin is explicitly set, validate against it.
	if h.AllowedOrigin != "" {
		allowed, err := url.Parse(h.AllowedOrigin)
		if err != nil {
			return false
		}

		reqOrigin, err := url.Parse(origin)
		if err != nil {
			return false
		}

		// Strict comparison: scheme, host, and port must match.
		if reqOrigin.Scheme != allowed.Scheme {
			return false
		}
		if reqOrigin.Host != allowed.Host {
			return false
		}
		return true
	}

	// No AllowedOrigin configured: fall back to checking Origin host matches request host.
	// This prevents cross-site WebSocket hijacking while allowing the browser's same-origin requests.
	// Scheme is intentionally NOT enforced here: UWAS may be deployed on plain HTTP
	// (internal panels, IP-only access), and a browser opening the dashboard over HTTP
	// will send Origin: http://... -- forcing https would break those deployments.
	// For HTTPS-only enforcement, set AllowedOrigin explicitly in config.
	reqOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Allow only if origin host matches request host (same-origin).
	return reqOrigin.Host == r.Host
}

// --- Minimal WebSocket implementation (no external dependency) ---

// WSConn wraps a hijacked connection for WebSocket framing.
type WSConn struct {
	rwc    io.ReadWriteCloser // underlying TCP conn (for Close)
	reader io.Reader          // buffered reader (may have pre-read data)
	writer io.Writer          // buffered writer
	wmu    sync.Mutex         // serializes writes: PTY pump, close-frame echo and Close run concurrently
}

// UpgradeWebSocket performs the HTTP→WebSocket handshake.
func (h *Handler) UpgradeWebSocket(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, fmt.Errorf("not a websocket request (Upgrade: %q)", r.Header.Get("Upgrade"))
	}

	// Strict origin check: prevent cross-site WebSocket hijacking
	if !h.CheckOrigin(r) {
		return nil, fmt.Errorf("origin %q not allowed", r.Header.Get("Origin"))
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

	// Use bufrw for reads (may have buffered client data) and conn for close
	return &WSConn{rwc: conn, reader: bufrw, writer: conn}, nil
}

const maxWSPayload = 64 * 1024 // 64KB max frame to prevent OOM

// maxWSControlPayload caps a control frame's body. RFC 6455 §5.5: "All control
// frames MUST have a payload length of 125 bytes or less and MUST NOT be
// fragmented."
const maxWSControlPayload = 125

// WebSocket opcodes (RFC 6455 §5.2). Only the control opcodes are named here —
// the data opcodes (0x0 continuation, 0x1 text, 0x2 binary) all mean "message
// data" to this server and are handled identically.
const (
	opClose = 0x8 // control
	opPing  = 0x9 // control
	opPong  = 0xA // control
)

func (c *WSConn) ReadMessage() ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return nil, err
	}

	opcode := header[0] & 0x0F
	masked := (header[1] & 0x80) != 0
	payloadLen := int(header[1] & 0x7F)

	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, err
		}
		payloadLen = int(ext[0])<<8 | int(ext[1])
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, err
		}
		// RFC 6455 §5.2: the most significant bit of the 64-bit length MUST be
		// 0. Reject it here. Reading the bytes into a signed int64 turned a
		// client-set high bit into a NEGATIVE payloadLen, which cannot satisfy
		// `payloadLen > maxWSPayload` below and so slipped past the 64KB cap —
		// reaching make([]byte, payloadLen) and panicking the process. That
		// panic is unrecovered (internal/terminal has no recover) and happens on
		// the WebSocket→PTY pump goroutine, not the net/http handler goroutine,
		// so one frame took down the whole server.
		if ext[0]&0x80 != 0 {
			return nil, fmt.Errorf("invalid frame: 64-bit length has high bit set")
		}
		payloadLen = int(ext[0])<<56 | int(ext[1])<<48 | int(ext[2])<<40 | int(ext[3])<<32 |
			int(ext[4])<<24 | int(ext[5])<<16 | int(ext[6])<<8 | int(ext[7])
	}

	if payloadLen > maxWSPayload {
		return nil, fmt.Errorf("frame too large: %d bytes", payloadLen)
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}

	// Control frames are protocol chatter and must never reach the PTY as
	// terminal input. RFC 6455 §5.5 keeps them out of the data stream; §5.5.2
	// additionally requires a Pong in response to every Ping.
	//
	// Ping and Pong previously fell through to `return payload, nil`, so the
	// WS->PTY pump wrote their payload straight into the user's interactive
	// shell, and a client using keepalive pings never got an answer.
	switch opcode {
	case opClose:
		c.writeClose(payload) // echo close frame body (status code)
		return nil, io.EOF
	case opPing:
		// RFC 6455 §5.5.2: the Pong MUST carry the Ping's application data.
		c.writePong(payload)
		return nil, nil
	case opPong:
		// An unsolicited/late Pong needs no reply; drop it, never forward it.
		return nil, nil
	}
	return payload, nil
}

// writeClose emits a Close frame (FIN | 0x8) carrying payload.
//
// The close echo used to go out through WriteText, which hardcodes 0x81 (FIN |
// 0x1). That sent the closing handshake as a *text* frame: RFC 6455 §5.5.1
// defines the handshake as Close frame <-> Close frame, so a conforming client
// read 0x1 as application data, never completed the handshake, and received the
// status code as if it were terminal text. Close() below already used 0x88, so
// the two close paths disagreed inside one file; both now go through here.
func (c *WSConn) writeClose(payload []byte) error {
	return c.writeControl(opClose, payload)
}

// writePong answers a Ping, echoing its application data verbatim as RFC 6455
// §5.5.2 requires.
func (c *WSConn) writePong(payload []byte) error {
	return c.writeControl(opPong, payload)
}

// writeControl emits a single control frame (FIN | opcode) with a short length
// prefix. Control frames are capped at 125 bytes by RFC 6455 §5.5, so an
// over-long body is dropped rather than emitted with a length byte that would
// be misread as an extended-length marker.
func (c *WSConn) writeControl(opcode byte, payload []byte) error {
	if len(payload) > maxWSControlPayload {
		payload = nil
	}
	frame := make([]byte, 0, 2+len(payload))
	frame = append(frame, 0x80|opcode)
	frame = append(frame, byte(len(payload)))
	frame = append(frame, payload...)

	c.wmu.Lock()
	_, err := c.writer.Write(frame)
	c.wmu.Unlock()
	return err
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
	c.wmu.Lock()
	_, err := c.writer.Write(frame)
	c.wmu.Unlock()
	return err
}

func (c *WSConn) Close() error {
	c.wmu.Lock()
	c.writer.Write([]byte{0x88, 0x00}) // close frame
	c.wmu.Unlock()
	return c.rwc.Close()
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
