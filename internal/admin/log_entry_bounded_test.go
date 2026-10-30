package admin

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// F49300: the access-log ring buffer behind GET /api/v1/logs kept the Host,
// Method, Path and User-Agent of every request exactly as the client sent
// them. The request path is only length-checked after the deferred recorder
// is registered (a 414 for a long path is recorded with that long path), and
// Host / User-Agent / Method may each take up the whole 1 MiB header budget,
// so one request pinned about 1 MiB and the 1000-entry ring about 1 GiB. The
// access log (F14800) and the blocked-request statistics (F48100) already
// bound the same fields; this ring did not.

func logEntryBytes(e LogEntry) int {
	return len(e.Host) + len(e.Method) + len(e.Path) + len(e.UserAgent) + len(e.Remote) + len(e.RemoteAddr)
}

func TestRecordLogBoundsClientControlledFields(t *testing.T) {
	big := strings.Repeat("a", 1<<20)
	s := &Server{}
	s.RecordLog(LogEntry{Host: big, Method: big, Path: "/" + big, UserAgent: big, Status: 414})

	got := s.logBuf.Snapshot()
	if len(got) != 1 {
		t.Fatalf("snapshot has %d entries, want 1", len(got))
	}
	const ceiling = 8 << 10 // generous for 255 + 32 + 4096 + 1024 plus markers
	n := logEntryBytes(got[0])
	t.Logf("EXPECTED: one stored entry holds at most %d bytes of client text\nACTUAL: %d bytes", ceiling, n)
	if n > ceiling {
		t.Fatalf("PROBLEM CONFIRMED: a single request pinned %d bytes in the log ring (host %d, method %d, path %d, user agent %d)",
			n, len(got[0].Host), len(got[0].Method), len(got[0].Path), len(got[0].UserAgent))
	}
	for name, v := range map[string]string{"host": got[0].Host, "method": got[0].Method, "path": got[0].Path, "user_agent": got[0].UserAgent} {
		if !utf8.ValidString(v) {
			t.Errorf("%s is not valid UTF-8 after clipping", name)
		}
		if !strings.Contains(v, "truncated") {
			t.Errorf("%s was shortened without a marker: %q", name, v[:min(len(v), 40)])
		}
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

// Clipping lands in the middle of a multi-byte rune: the stored text must
// stay valid UTF-8, because the entry is JSON-encoded for the dashboard.
func TestRecordLogClipKeepsRunesWhole(t *testing.T) {
	ua := strings.Repeat("é", 4096) // 8192 bytes, two bytes per rune
	s := &Server{}
	s.RecordLog(LogEntry{Host: "example.com", Method: "GET", Path: "/", UserAgent: ua, Status: 200})
	got := s.logBuf.Snapshot()[0].UserAgent
	if len(got) >= len(ua) {
		t.Fatalf("user agent was not clipped: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("clipped user agent is not valid UTF-8: %q", got[len(got)-30:])
	}
}

// Control: ordinary requests are stored exactly as recorded.
func TestRecordLogKeepsOrdinaryEntryIntact(t *testing.T) {
	want := LogEntry{
		Host:       "shop.example.com",
		Method:     "PROPFIND",
		Path:       "/wp-content/uploads/2026/10/photo.jpg",
		UserAgent:  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36",
		Status:     200,
		Bytes:      12345,
		DurationMS: 1.5,
		Duration:   "2ms",
		RemoteAddr: "203.0.113.9:51234",
		Remote:     "203.0.113.9",
	}
	s := &Server{}
	s.RecordLog(want)
	got := s.logBuf.Snapshot()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("ordinary entry changed: got %+v, want %+v", got, want)
	}

	// A path at exactly the bound is kept whole; one byte more is clipped.
	const pathBound = 4096
	atBound := "/" + strings.Repeat("p", pathBound-1)
	s.RecordLog(LogEntry{Path: atBound})
	over := "/" + strings.Repeat("p", pathBound)
	s.RecordLog(LogEntry{Path: over})
	snap := s.logBuf.Snapshot()
	if snap[1].Path != atBound {
		t.Errorf("path of exactly %d bytes was changed", pathBound)
	}
	if snap[2].Path == over || !strings.Contains(snap[2].Path, "truncated") {
		t.Errorf("path of %d bytes was not clipped", len(over))
	}
}
