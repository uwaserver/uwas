package terminal

// Regression guard for the WebSocket frame-length parser.
//
// ReadMessage assembled the 64-bit extended length into a SIGNED int64. A
// client that set the high bit (which RFC 6455 §5.2 requires to be 0) produced a
// NEGATIVE payloadLen, which cannot satisfy `payloadLen > maxWSPayload` — so it
// slipped past the documented 64KB cap straight into make([]byte, payloadLen),
// which panics. That panic is unrecovered (internal/terminal has no recover)
// and fires on the WebSocket→PTY pump goroutine rather than the net/http
// handler goroutine, so a single frame killed the whole server process.
//
// The contract these pin: a frame whose declared length is out of range must be
// REJECTED with an error, never panic. TestWSConnReadMessageTooLarge already
// covers the positive oversize case; these cover the negative branch and the
// exact-limit boundary the fix must not disturb.

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// wsExtFrame builds an unmasked frame with the given length code and extended
// length bytes.
func wsExtFrame(lengthCode byte, ext []byte, payload []byte) []byte {
	frame := []byte{0x82, lengthCode}
	frame = append(frame, ext...)
	frame = append(frame, payload...)
	return frame
}

// readFrameNoPanic calls ReadMessage and turns a panic into a test failure, so
// a regression reports as a readable message instead of killing the test binary.
func readFrameNoPanic(t *testing.T, frame []byte) ([]byte, error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ReadMessage panicked on a malformed frame: %v", r)
		}
	}()
	conn := &WSConn{reader: bytes.NewReader(frame), writer: io.Discard}
	return conn.ReadMessage()
}

// TestWSConnReadMessageRejectsNegativeExtendedLength covers the regression: the
// 127-form length whose high bit is set reads back as a negative int64.
func TestWSConnReadMessageRejectsNegativeExtendedLength(t *testing.T) {
	cases := []struct {
		name string
		ext  []byte
	}{
		{"high bit only", []byte{0x80, 0, 0, 0, 0, 0, 0, 0}},
		{"all bits set", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"high bit with tail set", []byte{0x80, 0xDE, 0xAD, 0xBE, 0xEF, 0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readFrameNoPanic(t, wsExtFrame(127, tc.ext, nil))
			if err == nil {
				t.Fatal("expected an error rejecting a 64-bit length with the high bit set, got nil")
			}
		})
	}
}

// TestWSConnReadMessageRejectsOversizeExtendedLength is the neighbouring branch
// the fix must leave intact: a positive length above the 64KB cap.
func TestWSConnReadMessageRejectsOversizeExtendedLength(t *testing.T) {
	ext := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x0D, 0x40} // 200000
	if _, err := readFrameNoPanic(t, wsExtFrame(127, ext, nil)); err == nil {
		t.Fatal("expected an error for an oversize 127-form length, got nil")
	}
}

// TestWSConnReadMessageAcceptsMaxExtendedLength pins the exact upper boundary:
// maxWSPayload (64KB) is still a legal frame. A fix that rejected the boundary
// itself, rather than only out-of-range values, would break the documented cap.
func TestWSConnReadMessageAcceptsMaxExtendedLength(t *testing.T) {
	payload := strings.Repeat("a", maxWSPayload)
	ext := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00} // 65536
	msg, err := readFrameNoPanic(t, wsExtFrame(127, ext, []byte(payload)))
	if err != nil {
		t.Fatalf("a frame of exactly maxWSPayload must be accepted, got error: %v", err)
	}
	if len(msg) != maxWSPayload {
		t.Fatalf("got %d bytes, want %d", len(msg), maxWSPayload)
	}
}

// TestWSConnReadMessageAcceptsValidFrames is the control: ordinary short and
// 126-form frames must still decode to their exact payload.
func TestWSConnReadMessageAcceptsValidFrames(t *testing.T) {
	t.Run("short frame", func(t *testing.T) {
		payload := []byte("echo hello")
		frame := append([]byte{0x82, byte(len(payload))}, payload...)
		msg, err := readFrameNoPanic(t, frame)
		if err != nil {
			t.Fatalf("valid short frame rejected: %v", err)
		}
		if !bytes.Equal(msg, payload) {
			t.Fatalf("got %q, want %q", msg, payload)
		}
	})

	t.Run("126-form frame", func(t *testing.T) {
		payload := []byte(strings.Repeat("b", 300)) // needs the extended 16-bit form
		frame := append([]byte{0x82, 126, byte(len(payload) >> 8), byte(len(payload))}, payload...)
		msg, err := readFrameNoPanic(t, frame)
		if err != nil {
			t.Fatalf("valid 126-form frame rejected: %v", err)
		}
		if !bytes.Equal(msg, payload) {
			t.Fatalf("got %d bytes, want %d", len(msg), len(payload))
		}
	})
}
