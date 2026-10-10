package cronjob

import (
	"strings"
	"testing"
)

// F1002: a job's captured stdout/stderr is stored whole in the record, the
// 100-entry history and cron_history.json, so a chatty command grows server
// memory and the persisted file without bound. Baseline: output is capped at
// 64 KiB (+ a short truncation marker).
func TestExecuteCapsRecordedOutput(t *testing.T) {
	const limit = 64 << 10
	m := NewMonitor("")
	// control: small output is kept verbatim
	if r := m.Execute("example.com", "* * * * *", "echo hello"); !r.Success || strings.TrimSpace(r.Output) != "hello" {
		t.Fatalf("control failed: %+v", r)
	}
	r := m.Execute("example.com", "* * * * *", "head -c 3000000 /dev/zero")
	if !r.Success {
		t.Fatalf("command failed (invalid proof): %+v", r)
	}
	if len(r.Output) > limit+256 {
		t.Fatalf("EXPECTED: output <= %d bytes\nACTUAL: %d bytes\nPROBLEM CONFIRMED", limit+256, len(r.Output))
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

func TestCappedBufferBoundary(t *testing.T) {
	var c cappedBuffer
	if n, err := c.Write(make([]byte, maxRecordedOutput)); n != maxRecordedOutput || err != nil || c.truncated {
		t.Fatalf("exact fit: n=%d err=%v truncated=%v", n, err, c.truncated)
	}
	if n, err := c.Write([]byte("x")); n != 1 || err != nil || !c.truncated {
		t.Fatalf("overflow must report a full write and mark truncation: n=%d err=%v", n, err)
	}
	if c.Len() != maxRecordedOutput || !strings.HasSuffix(c.String(), "[output truncated]") {
		t.Fatalf("len=%d tail=%q", c.Len(), c.String()[c.Len():])
	}
	var s cappedBuffer
	s.Write([]byte("ab"))
	if s.String() != "ab" || s.Len() != 2 {
		t.Fatalf("small write altered: %q", s.String())
	}
	t.Log("FIX VERIFIED")
}
