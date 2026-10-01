//go:build linux

package terminal

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// A PTY is a byte stream, not a character stream: a multi-byte rune can be
// split across two master.Read() calls. The pump must carry such a trailing
// prefix into the next read instead of handing it to utf8.Valid, which reports
// a partial sequence as invalid and lets sanitizeUTF8 replace its bytes with
// '?' — corrupting text the shell actually produced.

// utf8SplitMarker is the tail sentinel proving the whole payload came back.
const utf8SplitMarker = "UTF8SPLITEND"

// utf8SplitRunes is how many 3-byte runes the payload carries. A PTY read
// boundary falls wherever the kernel happens to split the byte stream, so a
// small payload only corrupts a rune when the boundary lands mid-character by
// chance. Carrying many runes makes at least one split near-certain: every
// boundary has a ~2/3 chance of landing inside a 3-byte rune, and across the
// ~10 reads this payload spans the chance of none is negligible.
const utf8SplitRunes = 7000

// utf8SplitPayload builds a large line of 3-byte runes bracketed by markers.
// If the pump applies utf8.Valid to a per-read chunk, every rune that a read
// boundary splits is replaced byte-for-byte with '?'.
func utf8SplitPayload() []byte {
	return []byte("UTF8SPLITSTART" + strings.Repeat("世", utf8SplitRunes) + utf8SplitMarker + "\n")
}

// TestPumpPreservesMultiByteRuneSplitAcrossReads drives the real ServeHTTP over
// a real TCP listener, a real WebSocket handshake and a real PTY. A complete,
// valid multi-byte character must reach the client intact no matter where the
// kernel splits the reads.
func TestPumpPreservesMultiByteRuneSplitAcrossReads(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY bridge only on Linux")
	}
	// /bin/cat echoes stdin back through the PTY, exercising the real pump.
	h := &Handler{Shell: "/bin/cat", Logger: logger.New("error", "text")}
	cli, cleanup := dialTerminal(t, h)
	defer cleanup()

	if err := cli.writeFrame(utf8SplitPayload()); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := cli.readTextUntil(t, utf8SplitMarker, 20*time.Second)
	if !strings.Contains(got, utf8SplitMarker) {
		t.Fatalf("bridge did not deliver the tail marker; got %d bytes", len(got))
	}
	if n := strings.Count(got, "?"); n != 0 {
		t.Errorf("valid multi-byte runes were corrupted into %d '?' substitutions; "+
			"runes that a read boundary split must be carried into the next read "+
			"instead of sanitized", n)
	}
}

// TestIncompleteUTF8LenBoundaries pins the helper that decides how much of a
// read is a carried-over rune prefix. 0 means "ends on a character boundary".
func TestIncompleteUTF8LenBoundaries(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want int
	}{
		{"empty", []byte{}, 0},
		{"ascii_only", []byte("hello"), 0},
		{"complete_2byte", []byte("héllo"), 0},
		{"complete_3byte", []byte("世界"), 0},
		{"complete_4byte", []byte("😀"), 0},
		// A rune cut short by the read boundary must be held back.
		{"split_2byte_1_of_2", []byte{'a', 0xc3}, 1},
		{"split_3byte_1_of_3", []byte{'a', 0xe4}, 1},
		{"split_3byte_2_of_3", []byte{'a', 0xe4, 0xb8}, 2},
		{"split_4byte_2_of_4", []byte{0xf0, 0x9f}, 2},
		{"split_4byte_3_of_4", []byte{0xf0, 0x9f, 0x98}, 3},
		// A leading continuation byte has no lead within reach, so it is not a
		// pending prefix — utf8.Valid rejects it as invalid instead.
		{"stray_continuation", []byte{'a', 0x80}, 0},
		// 0xFF is not a valid lead byte, so it must not be treated as pending.
		{"invalid_lead_ff", []byte{'a', 0xff}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := incompleteUTF8Len(tt.in); got != tt.want {
				t.Errorf("incompleteUTF8Len(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// Control: a short multi-byte payload fits inside one read and must pass
// through untouched, on both the current and the fixed code.
func TestPumpKeepsShortMultiBytePayloadIntact(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY bridge only on Linux")
	}
	h := &Handler{Shell: "/bin/cat", Logger: logger.New("error", "text")}
	cli, cleanup := dialTerminal(t, h)
	defer cleanup()

	if err := cli.writeFrame([]byte("héllo→世界\n")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := cli.readTextUntil(t, "世界", 6*time.Second)
	if !strings.Contains(got, "héllo→世界") {
		t.Errorf("short multi-byte payload must pass unchanged; got %q", got)
	}
}

// Control: the sanitize branch still exists for genuinely invalid bytes, which
// must keep being replaced one-for-one with '?'.
func TestPumpStillSanitizesGenuinelyInvalidBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTY bridge only on Linux")
	}
	h := &Handler{Shell: "/bin/cat", Logger: logger.New("error", "text")}
	cli, cleanup := dialTerminal(t, h)
	defer cleanup()

	if err := cli.writeFrame([]byte{'A', 'B', 0xff, 0xfe, 'C', 'D', '\n'}); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := cli.readTextUntil(t, "D", 6*time.Second)
	// Each invalid byte becomes exactly one '?'; \r\n is the PTY's own
	// line-ending translation, not part of the payload.
	if !strings.Contains(got, "AB??CD") {
		t.Errorf("genuinely invalid bytes must be replaced one-for-one with '?' "+
			"and surrounding text preserved; got %q", got)
	}
}
