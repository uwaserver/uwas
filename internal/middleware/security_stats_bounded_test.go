package middleware

import (
	"strings"
	"testing"
)

// F48100: SecurityStats keeps the last maxRecentBlocked rejections and the
// dashboard serves them. It stored the User-Agent and path exactly as sent, and
// a request may carry a header block of up to max_header_bytes (1 MiB by
// default). Anyone who could get requests rejected — a WAF hit, a bot-guard
// block — could therefore pin about maxRecentBlocked x 1 MiB (200 MiB) in the
// ring and make the security overview answer with that much. The access log
// already clips both (F14800); this record did not.
func TestSecurityStatsBoundsAttackerSuppliedFields(t *testing.T) {
	s := NewSecurityStats()
	hugeUA := strings.Repeat("A", 1<<20)
	hugePath := "/" + strings.Repeat("p", 64<<10)
	for i := 0; i < maxRecentBlocked*2; i++ {
		s.Record("203.0.113.9:4000", hugePath, "waf", hugeUA)
	}

	var uaBytes, pathBytes, longest int
	for _, e := range s.RecentBlocked() {
		uaBytes += len(e.UA)
		pathBytes += len(e.Path)
		if len(e.UA) > longest {
			longest = len(e.UA)
		}
	}
	const ceiling = maxRecentBlocked * (maxLogURIBytes + maxLogHeaderBytes + 256) // path + UA + truncation markers
	t.Logf("EXPECTED: retained ua+path bytes <= %d (each field clipped)\nACTUAL: ua=%d path=%d (longest UA %d)", ceiling, uaBytes, pathBytes, longest)
	if uaBytes+pathBytes > ceiling {
		t.Fatalf("PROBLEM CONFIRMED: %d bytes of attacker-supplied text retained in the ring, ceiling %d", uaBytes+pathBytes, ceiling)
	}
}

// Ordinary values are kept as they are, so the dashboard still shows the real
// User-Agent and path of a blocked request.
func TestSecurityStatsKeepsOrdinaryFields(t *testing.T) {
	s := NewSecurityStats()
	const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 sqlmap/1.7"
	s.Record("203.0.113.9:4000", "/wp-login.php?x=1", "bot", ua)
	got := s.RecentBlocked()
	if len(got) != 1 || got[0].UA != ua || got[0].Path != "/wp-login.php?x=1" || got[0].IP != "203.0.113.9" {
		t.Fatalf("ordinary record changed: %+v", got)
	}
}
