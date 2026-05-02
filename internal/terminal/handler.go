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
		// WebSocket connections SHOULD have an Origin header per RFC 6454.
		// Reject connections without origin to prevent cross-site hijacking.
		// Some clients (non-browser) may not send Origin — only allow if
		// AllowedOrigin is explicitly configured to accept such connections.
		if h.AllowedOrigin == "" {
			return false
		}
		return true
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
	reqOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Reject if origin scheme is not https (WebSocket should only be used over HTTPS).
	if reqOrigin.Scheme != "https" {
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

	if opcode == 0x8 { // close frame — echo back per RFC 6455
		c.WriteText(payload) // echo close frame body (status code)
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
	_, err := c.writer.Write(frame)
	return err
}

func (c *WSConn) Close() error {
	c.writer.Write([]byte{0x88, 0x00}) // close frame
	return c.rwc.Close()
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
