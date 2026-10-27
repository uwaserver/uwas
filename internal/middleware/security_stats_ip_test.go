package middleware

// F34601: the WAF and the bot guard handed r.RemoteAddr ("ip:port") to
// SecurityStats.Record, while every other rejection reason recorded the bare
// IP. The dashboard's blocked-requests table therefore showed "203.0.113.9:0"
// (or a random source port) for WAF/bot rows and "203.0.113.9" for the rest.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func TestSecurityStatsRecordsBareIP(t *testing.T) {
	cases := []struct{ in, want string }{
		{"203.0.113.9:51234", "203.0.113.9"},
		{"203.0.113.9:0", "203.0.113.9"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		// controls: already bare
		{"203.0.113.9", "203.0.113.9"},
		{"2001:db8::1", "2001:db8::1"},
	}
	for _, c := range cases {
		stats := NewSecurityStats()
		var observed string
		stats.SetObserver(func(ip, _ string) { observed = ip })
		stats.Record(c.in, "/x", "waf", "ua")
		got := stats.RecentBlocked()
		if len(got) != 1 || got[0].IP != c.want {
			t.Errorf("Record(%q): stored IP = %+v, want %q", c.in, got, c.want)
		}
		if observed != c.want {
			t.Errorf("Record(%q): observer got %q, want %q", c.in, observed, c.want)
		}
	}
}

func TestBotGuardRecordsBareIP(t *testing.T) {
	stats := NewSecurityStats()
	h := BotGuard(logger.New("error", "text"), stats)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:51234"
	req.Header.Set("User-Agent", "sqlmap/1.7")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("control: bot not blocked, status %d", rec.Code)
	}
	if got := stats.RecentBlocked(); len(got) != 1 || got[0].IP != "203.0.113.9" {
		t.Errorf("recorded IP = %+v, want bare 203.0.113.9", got)
	}
}
