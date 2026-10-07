package terminal

// Regression guard for the WebSocket closing handshake.
//
// ReadMessage handled an inbound Close frame (0x8) by echoing it through
// WriteText, which hardcodes 0x81 (FIN | 0x1). That sent the closing handshake
// as a TEXT frame. RFC 6455 §5.5.1 defines the handshake as Close frame <->
// Close frame, so a conforming client read the echo as application data, never
// completed the handshake, and got the status code rendered as terminal text.
//
// WSConn.Close() already emitted the correct 0x88, so the two close paths
// disagreed inside one file. Both now go through writeClose.
//
// These tests pin the opcode, that the status code survives, and that the text
// path (which legitimately uses 0x81) is untouched.

import (
	"bytes"
	"io"
	"testing"
)

// buildFrame builds an unmasked WebSocket frame with the given opcode.
// Payloads of 126+ bytes need the RFC 6455 extended 16-bit length form;
// otherwise ReadMessage consumes the first two payload bytes as a length and
// the frame does not parse.
func buildFrame(opcode byte, payload []byte) []byte {
	n := len(payload)
	switch {
	case n < 126:
		return append([]byte{0x80 | opcode, byte(n)}, payload...)
	case n < 65536:
		return append([]byte{0x80 | opcode, 126, byte(n >> 8), byte(n)}, payload...)
	default:
		hdr := []byte{0x80 | opcode, 127, 0, 0, 0, 0,
			byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
		return append(hdr, payload...)
	}
}

// closeEcho feeds one frame through the real ReadMessage and returns the bytes
// the server wrote back.
func closeEcho(t *testing.T, in []byte) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	conn := &WSConn{reader: bytes.NewReader(in), writer: &out}
	_, err := conn.ReadMessage()
	if out.Len() == 0 {
		t.Fatalf("server wrote no echo frame (err=%v)", err)
	}
	return out.Bytes(), err
}

// TestCloseFrameIsEchoedAsCloseFrame is the regression guard: the echo of a
// Close frame must itself be a Close frame.
func TestCloseFrameIsEchoedAsCloseFrame(t *testing.T) {
	// Status 1000 (normal closure).
	written, err := closeEcho(t, buildFrame(0x8, []byte{0x03, 0xE8}))

	if got := written[0]; got != 0x88 {
		t.Fatalf("close echo used opcode 0x%X, want 0x88 (FIN|close). RFC 6455 §5.5.1 "+
			"requires the handshake to be Close<->Close; 0x81 is read as app data. "+
			"Raw echo: % X (err=%v)", got, written, err)
	}
}

// TestCloseEchoPreservesStatusCode pins that the fix changed the opcode only —
// the status code must still reach the peer.
func TestCloseEchoPreservesStatusCode(t *testing.T) {
	written, _ := closeEcho(t, buildFrame(0x8, []byte{0x03, 0xE8}))

	if len(written) < 4 || written[1] != 2 || written[2] != 0x03 || written[3] != 0xE8 {
		t.Fatalf("close echo dropped the status code: % X", written)
	}
}

// TestCloseEchoWithEmptyBody covers the other legal shape: a Close with no
// payload must echo as a bare two-byte Close frame.
func TestCloseEchoWithEmptyBody(t *testing.T) {
	written, _ := closeEcho(t, buildFrame(0x8, nil))

	if len(written) != 2 || written[0] != 0x88 || written[1] != 0 {
		t.Fatalf("empty-body close echo = % X, want 88 00", written)
	}
}

// TestCloseEchoDropsOversizeBody covers the guard on an over-long control body.
// A Close payload above 125 bytes cannot use the short length form; emitting it
// anyway would produce a malformed frame, so the echo drops the body.
func TestCloseEchoDropsOversizeBody(t *testing.T) {
	written, _ := closeEcho(t, buildFrame(0x8, bytes.Repeat([]byte{0xAB}, 126)))

	if written[0] != 0x88 {
		t.Fatalf("oversize-body close echo opcode 0x%X, want 0x88", written[0])
	}
	if written[1] != 0 {
		t.Fatalf("oversize body should be dropped, got length %d", written[1])
	}
}

// TestTextFrameStillUsesTextOpcode is the control: the text path legitimately
// uses 0x81, and must keep doing so. If this fails, the close fix over-reached.
func TestTextFrameStillUsesTextOpcode(t *testing.T) {
	var out bytes.Buffer
	conn := &WSConn{reader: bytes.NewReader(buildFrame(0x1, []byte("hi"))), writer: &out}

	msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("text frame rejected: %v", err)
	}
	if string(msg) != "hi" {
		t.Fatalf("got %q want %q", msg, "hi")
	}
	if err := conn.WriteText([]byte("hi")); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if got := out.Bytes()[0]; got != 0x81 {
		t.Fatalf("WriteText opcode = 0x%X, want 0x81", got)
	}
}

// TestCloseMethodUsesCloseOpcode records that Close() and the close echo agree,
// which is what the fix was for.
func TestCloseMethodUsesCloseOpcode(t *testing.T) {
	var out bytes.Buffer
	conn := &WSConn{writer: &out, rwc: stubRWC{}}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := out.Bytes()[0]; got != 0x88 {
		t.Fatalf("Close() opcode 0x%X, want 0x88", got)
	}
}

// stubRWC satisfies io.ReadWriteCloser so Close() can run without a connection.
type stubRWC struct{}

func (stubRWC) Read([]byte) (int, error)    { return 0, io.EOF }
func (stubRWC) Write(p []byte) (int, error) { return len(p), nil }
func (stubRWC) Close() error                { return nil }
