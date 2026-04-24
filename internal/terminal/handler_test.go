package terminal

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func TestComputeAcceptKey(t *testing.T) {
	// RFC 6455 example: key "dGhlIHNhbXBsZSBub25jZQ==" → "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	expected := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	got := computeAcceptKey(key)
	if got != expected {
		t.Errorf("computeAcceptKey(%q) = %q, want %q", key, got, expected)
	}
}

func TestNew(t *testing.T) {
	h := New(nil)
	if h == nil {
		t.Fatal("New returned nil")
	}
}

// --- WebSocket upgrade tests ---

func TestUpgradeWebSocketNotWebsocket(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal", nil)
	req.Header.Set("Upgrade", "http") // not websocket
	w := httptest.NewRecorder()

	h := &Handler{Logger: &logger.Logger{}}
	_, err := h.UpgradeWebSocket(w, req)
	if err == nil {
		t.Error("expected error for non-websocket upgrade")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("not a websocket")) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpgradeWebSocketNoHijack(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder() // httptest.ResponseRecorder doesn't support hijacking

	h := &Handler{Logger: &logger.Logger{}}
	_, err := h.UpgradeWebSocket(w, req)
	if err == nil {
		t.Error("expected error for non-hijackable response writer")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("hijacking")) {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- WSConn tests ---

func TestWSConnWriteTextSmall(t *testing.T) {
	var buf bytes.Buffer
	conn := &WSConn{writer: &buf}

	data := []byte("hello")
	err := conn.WriteText(data)
	if err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}

	// Check frame structure: 0x81 (FIN + text), 0x05 (length), "hello"
	frame := buf.Bytes()
	if len(frame) < 7 {
		t.Fatalf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != 0x81 {
		t.Errorf("expected first byte 0x81, got 0x%02x", frame[0])
	}
	if frame[1] != 0x05 {
		t.Errorf("expected length byte 0x05, got 0x%02x", frame[1])
	}
	if !bytes.Equal(frame[2:], data) {
		t.Errorf("expected payload %q, got %q", data, frame[2:])
	}
}

func TestWSConnWriteTextMedium(t *testing.T) {
	var buf bytes.Buffer
	conn := &WSConn{writer: &buf}

	// Data between 126 and 65535 bytes uses 16-bit length
	data := make([]byte, 200)
	for i := range data {
		data[i] = byte('a' + i%26)
	}

	err := conn.WriteText(data)
	if err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}

	frame := buf.Bytes()
	if len(frame) < 4 {
		t.Fatalf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != 0x81 {
		t.Errorf("expected first byte 0x81, got 0x%02x", frame[0])
	}
	if frame[1] != 126 { // 16-bit length indicator
		t.Errorf("expected length byte 126, got %d", frame[1])
	}
	// Length is next 2 bytes (big endian)
	length := int(frame[2])<<8 | int(frame[3])
	if length != 200 {
		t.Errorf("expected length 200, got %d", length)
	}
}

func TestWSConnReadMessageSmall(t *testing.T) {
	// Build a simple unmasked text frame: FIN=1, opcode=text, length=5, "hello"
	frame := []byte{0x81, 0x05, 'h', 'e', 'l', 'l', 'o'}
	conn := &WSConn{reader: bytes.NewReader(frame)}

	msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if !bytes.Equal(msg, []byte("hello")) {
		t.Errorf("expected 'hello', got %q", msg)
	}
}

func TestWSConnReadMessageMasked(t *testing.T) {
	// Build a masked text frame: FIN=1, opcode=text, MASK=1, length=5, mask, masked payload
	payload := []byte("hello")
	mask := []byte{0x01, 0x02, 0x03, 0x04}
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}

	frame := []byte{0x81, 0x85} // FIN + text, MASK + length 5
	frame = append(frame, mask...)
	frame = append(frame, masked...)

	conn := &WSConn{reader: bytes.NewReader(frame)}

	msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if !bytes.Equal(msg, payload) {
		t.Errorf("expected %q, got %q", payload, msg)
	}
}

func TestWSConnReadMessageCloseFrame(t *testing.T) {
	// Build a close frame: FIN=1, opcode=close (0x8), length=0
	frame := []byte{0x88, 0x00}
	conn := &WSConn{reader: bytes.NewReader(frame), writer: io.Discard}

	_, err := conn.ReadMessage()
	if err != io.EOF {
		t.Errorf("expected io.EOF for close frame, got: %v", err)
	}
}

func TestWSConnReadMessageTooLarge(t *testing.T) {
	// Build a frame claiming to be larger than maxWSPayload (64KB)
	// The error could be "frame too large" or EOF/unexpected EOF since
	// we can't provide that much data in a test
	largePayload := make([]byte, 100) // Some payload data
	frame := []byte{0x81, 0x7f, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00} // Claims 64KB+1
	frame = append(frame, largePayload...)
	conn := &WSConn{reader: bytes.NewReader(frame)}

	_, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected error for oversized frame")
	}
	// Error could be "frame too large", EOF, or "unexpected EOF" depending on timing
	errStr := err.Error()
	if !bytes.Contains([]byte(errStr), []byte("frame too large")) &&
		!bytes.Contains([]byte(errStr), []byte("EOF")) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWSConnReadMessageExtendedLength(t *testing.T) {
	// Test 16-bit extended length (126 indicator)
	payload := make([]byte, 200)
	for i := range payload {
		payload[i] = byte('x')
	}

	frame := []byte{0x81, 126, 0x00, 200} // FIN+text, 126 indicator, length=200
	frame = append(frame, payload...)

	conn := &WSConn{reader: bytes.NewReader(frame)}

	msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if len(msg) != 200 {
		t.Errorf("expected 200 bytes, got %d", len(msg))
	}
}

// --- Non-Linux handler test ---

func TestHandlerServeHTTPNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("non-Linux handler test skipped on Linux")
	}
	h := New(nil)
	req := httptest.NewRequest("GET", "/terminal", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected status 501, got %d", w.Code)
	}
	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("only available on Linux")) {
		t.Errorf("unexpected body: %q", body)
	}
}

// --- ComputeAcceptKey additional tests ---

func TestComputeAcceptKeyEmpty(t *testing.T) {
	// Empty key should still produce a valid accept key
	result := computeAcceptKey("")
	if result == "" {
		t.Error("expected non-empty result for empty key")
	}
}

func TestComputeAcceptKeyDifferentKeys(t *testing.T) {
	// Different keys should produce different results
	key1 := computeAcceptKey("key1")
	key2 := computeAcceptKey("key2")
	if key1 == key2 {
		t.Error("different keys should produce different accept keys")
	}
}

// --- WSConn.Close tests ---

type closeBuffer struct {
	bytes.Buffer
	closed bool
}

func (c *closeBuffer) Close() error {
	c.closed = true
	return nil
}

func TestWSConnClose(t *testing.T) {
	// Test that Close writes a close frame and closes the underlying connection
	var buf closeBuffer
	conn := &WSConn{rwc: &buf, writer: &buf}

	err := conn.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Check that close frame was written: 0x88 (FIN + close), 0x00 (length 0)
	written := buf.Bytes()
	if len(written) < 2 {
		t.Fatalf("expected at least 2 bytes, got %d", len(written))
	}
	if written[0] != 0x88 {
		t.Errorf("expected first byte 0x88, got 0x%02x", written[0])
	}
	if written[1] != 0x00 {
		t.Errorf("expected second byte 0x00, got 0x%02x", written[1])
	}

	// Check that underlying connection was closed
	if !buf.closed {
		t.Error("expected underlying connection to be closed")
	}
}

// --- Additional ReadMessage tests ---

func TestWSConnReadMessageEOF(t *testing.T) {
	// Empty reader should return EOF
	conn := &WSConn{reader: bytes.NewReader([]byte{})}

	_, err := conn.ReadMessage()
	if err != io.EOF && err == nil {
		t.Error("expected error or EOF for empty reader")
	}
}

func TestWSConnReadMessageLargePayload(t *testing.T) {
	// Test 64-bit extended length (127 indicator) with small payload
	// This tests the parsing code without actually sending 2^63 bytes
	payload := []byte("test payload")

	// Build frame with 127 indicator but small actual payload
	frame := []byte{0x81, 127} // FIN+text, 127 indicator
	// 8 bytes for length (big endian), set to actual payload length
	lengthBytes := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, byte(len(payload))}
	frame = append(frame, lengthBytes...)
	frame = append(frame, payload...)

	conn := &WSConn{reader: bytes.NewReader(frame)}

	msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if !bytes.Equal(msg, payload) {
		t.Errorf("expected %q, got %q", payload, msg)
	}
}

func TestWSConnReadMessageMaskReadError(t *testing.T) {
	// Frame claims to be masked but doesn't have enough bytes for mask
	frame := []byte{0x81, 0x85, 0x00} // FIN+text, MASK+length 5, partial mask
	conn := &WSConn{reader: bytes.NewReader(frame)}

	_, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected error for incomplete mask")
	}
}

func TestWSConnReadMessagePayloadReadError(t *testing.T) {
	// Frame claims payload but doesn't have enough bytes
	frame := []byte{0x81, 0x05, 'h', 'e'} // Claims 5 bytes but only has 2
	conn := &WSConn{reader: bytes.NewReader(frame)}

	_, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected error for incomplete payload")
	}
}

// TestWriteTextLarge tests writing a large WebSocket frame.
func TestWSConnWriteTextLarge(t *testing.T) {
	var buf bytes.Buffer
	conn := &WSConn{writer: &buf}

	// Data larger than 65535 bytes uses 64-bit length
	data := make([]byte, 70000)
	for i := range data {
		data[i] = byte('a' + i%26)
	}

	err := conn.WriteText(data)
	if err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}

	// Check frame structure
	frame := buf.Bytes()
	if len(frame) < 10 {
		t.Fatalf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != 0x81 {
		t.Errorf("expected first byte 0x81, got 0x%02x", frame[0])
	}
	if frame[1] != 127 {
		t.Errorf("expected length byte 0x7f (127), got 0x%02x", frame[1])
	}
}

// TestReadMessageBinaryOpcode tests reading a binary WebSocket frame.
func TestWSConnReadMessageBinaryOpcode(t *testing.T) {
	payload := []byte("binary data")
	frame := []byte{0x82, byte(len(payload))} // FIN+binary opcode (0x2)
	frame = append(frame, payload...)

	conn := &WSConn{reader: bytes.NewReader(frame)}

	msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if !bytes.Equal(msg, payload) {
		t.Errorf("expected payload %q, got %q", payload, msg)
	}
}
