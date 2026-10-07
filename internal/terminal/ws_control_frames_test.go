package terminal

// Regression guard for WebSocket CONTROL frame handling.
//
// ReadMessage handled exactly one opcode: close (0x8). Ping (0x9) and Pong
// (0xA) fell through to `return payload, nil`, so the WS->PTY pump wrote their
// payload straight into the user's interactive shell as stdin, and a client
// using keepalive pings never received the Pong that RFC 6455 §5.5.2 requires.
//
//   - §5.5 — control frames carry control data and are not part of the message
//     stream; they must never be surfaced as application data.
//   - §5.5.2 — "Upon receipt of a Ping frame, an endpoint MUST send a Pong
//     frame in response", carrying the Ping's application data.
//   - §5.5 — control frame bodies are capped at 125 bytes and MUST NOT be
//     fragmented.
//
// The data path (text/binary/continuation) must be untouched, and the close
// echo fixed in an earlier round must keep working.

import (
	"bytes"
	"testing"
)

// data opcodes, which ReadMessage treats as message content
const (
	testOpContinuation = 0x0
	testOpText         = 0x1
	testOpBinary       = 0x2
)

// wsCtrlFrame builds an unmasked frame; ReadMessage accepts those, which keeps
// the fixture readable.
func wsCtrlFrame(opcode byte, payload []byte) []byte {
	n := len(payload)
	var f []byte
	switch {
	case n < 126:
		f = []byte{0x80 | opcode, byte(n)}
	case n < 65536:
		f = []byte{0x80 | opcode, 126, byte(n >> 8), byte(n)}
	default:
		f = []byte{0x80 | opcode, 127, 0, 0, 0, 0,
			byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
	return append(f, payload...)
}

// feedFrame runs one frame through the real ReadMessage.
func feedFrame(frame []byte) (data []byte, written []byte, err error) {
	var out bytes.Buffer
	conn := &WSConn{reader: bytes.NewReader(frame), writer: &out}
	data, err = conn.ReadMessage()
	return data, out.Bytes(), err
}

// TestPingIsAnsweredWithPong is the regression guard: RFC 6455 §5.5.2 makes the
// Pong mandatory, and it must echo the Ping's application data.
func TestPingIsAnsweredWithPong(t *testing.T) {
	data, written, err := feedFrame(wsCtrlFrame(opPing, []byte("keepalive")))

	if len(written) == 0 {
		t.Fatalf("Ping went unanswered; RFC 6455 §5.5.2 requires a Pong. "+
			"surfaced data=%q err=%v", data, err)
	}
	if got := written[0] & 0x0F; got != opPong {
		t.Fatalf("Ping answered with opcode 0x%X, want Pong 0x%X. Raw: % X",
			got, opPong, written)
	}
	if !bytes.Contains(written, []byte("keepalive")) {
		t.Fatalf("Pong did not echo the Ping's application data: % X", written)
	}
}

// TestPingPayloadIsNotDeliveredAsTerminalInput is the other half: a control
// frame's payload must never reach the PTY. A trailing newline makes the
// consequence concrete — it would press Enter in the user's shell.
func TestPingPayloadIsNotDeliveredAsTerminalInput(t *testing.T) {
	data, _, err := feedFrame(wsCtrlFrame(opPing, []byte("whoami\n")))

	if len(data) != 0 {
		t.Fatalf("Ping payload %q surfaced as application data; it would be written "+
			"to the PTY as stdin by pty_linux.go (err=%v)", data, err)
	}
}

// TestPongPayloadIsNotDeliveredAsTerminalInput covers the sibling branch.
func TestPongPayloadIsNotDeliveredAsTerminalInput(t *testing.T) {
	data, _, err := feedFrame(wsCtrlFrame(opPong, []byte("hb-token\n")))

	if len(data) != 0 {
		t.Fatalf("Pong payload %q surfaced as application data (err=%v)", data, err)
	}
}

// TestPongIsNotAnswered guards against a Pong-Pong feedback loop: a Pong is
// never itself answered.
func TestPongIsNotAnswered(t *testing.T) {
	_, written, _ := feedFrame(wsCtrlFrame(opPong, nil))

	if len(written) != 0 {
		t.Fatalf("a Pong must not be answered, got % X", written)
	}
}

// TestOversizeControlBodyIsDropped pins RFC 6455 §5.5's 125-byte control cap: a
// control frame larger than that cannot be echoed with a short length byte, so
// the body is dropped rather than emitting a malformed frame.
func TestOversizeControlBodyIsDropped(t *testing.T) {
	_, written, _ := feedFrame(wsCtrlFrame(opPing, bytes.Repeat([]byte{0xAB}, 200)))

	if len(written) == 0 {
		t.Fatal("oversize Ping produced no reply")
	}
	if body := int(written[1] & 0x7F); body > maxWSControlPayload {
		t.Fatalf("control reply body %d bytes exceeds the %d-byte cap: % X",
			body, maxWSControlPayload, written)
	}
}

// TestDataOpcodesStillDelivered is the control for the whole fix: ordinary
// data frames must still reach the caller untouched and must not trigger any
// control reply. A "fix" that swallowed real terminal input would be worse than
// the bug.
func TestDataOpcodesStillDelivered(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opcode  byte
		payload []byte
	}{
		{"text", testOpText, []byte("ls -la\n")},
		{"binary", testOpBinary, []byte{0x00, 0xFF, 0x10}},
		{"continuation", testOpContinuation, []byte("tail")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, written, err := feedFrame(wsCtrlFrame(tc.opcode, tc.payload))
			if err != nil {
				t.Fatalf("data frame rejected: %v", err)
			}
			if !bytes.Equal(data, tc.payload) {
				t.Fatalf("delivered %q, want %q", data, tc.payload)
			}
			if len(written) != 0 {
				t.Fatalf("data frame produced a control reply: % X", written)
			}
		})
	}
}

// TestCloseStillEchoesClose keeps the earlier close-handshake fix honest: adding
// control-frame handling must not regress it.
func TestCloseStillEchoesClose(t *testing.T) {
	_, written, _ := feedFrame(wsCtrlFrame(opClose, []byte{0x03, 0xE8}))

	if len(written) == 0 {
		t.Fatal("close produced no echo")
	}
	if got := written[0] & 0x0F; got != opClose {
		t.Fatalf("close echoed with opcode 0x%X, want 0x%X", got, opClose)
	}
}
