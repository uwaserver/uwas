package admin

import (
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
)

// mcpCallParkedInRLock reports whether a goroutine is blocked acquiring
// configMu's read lock inside handleMCPCall.
func mcpCallParkedInRLock() bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "handleMCPCall") && strings.Contains(g, "sync.(*RWMutex).RLock") {
			return true
		}
	}
	return false
}

// TestMCPCallWaitsForConfigWriter pins that MCP tools, which read the shared
// *config.Config, are serialised against admin writers holding configMu.
// Gated order without sleeps: the writer holds the lock until the MCP call is
// observed parked in RLock, then mutates Domains; the result must include it.
func TestMCPCallWaitsForConfigWriter(t *testing.T) {
	s := testServer()
	s.SetMCP(mcp.New(s.config, logger.New("error", "text"), s.metrics))

	done := make(chan *httptest.ResponseRecorder, 1)
	s.configMu.Lock()
	go func() {
		rec := httptest.NewRecorder()
		s.handleMCPCall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/mcp/call",
			strings.NewReader(`{"name":"domain_list","input":{}}`))))
		done <- rec
	}()
	for !mcpCallParkedInRLock() {
		select {
		case <-done:
			s.configMu.Unlock()
			t.Fatal("MCP call completed while the config write lock was held")
		default:
		}
		runtime.Gosched()
	}
	s.config.Domains = append(s.config.Domains, config.Domain{Host: "added.example", Type: "static"})
	s.configMu.Unlock()

	rec := <-done
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "added.example") {
		t.Fatalf("code=%d body=%s, want 200 including the writer's domain", rec.Code, rec.Body.String())
	}
	if !s.configMu.TryLock() {
		t.Fatal("configMu still held after the MCP call")
	}
	s.configMu.Unlock()
}

// TestWebhookDeleteDoesNotRewriteReaderSnapshot pins that delete builds a new
// slice: handleWebhookList iterates a snapshot after releasing the read lock.
func TestWebhookDeleteDoesNotRewriteReaderSnapshot(t *testing.T) {
	s := testServer()
	s.config.Global.Webhooks = []config.WebhookConfig{
		{URL: "https://8.8.8.8/a"}, {URL: "https://8.8.8.8/b"}, {URL: "https://8.8.8.8/c"},
	}
	s.configMu.RLock()
	snap := s.config.Global.Webhooks
	s.configMu.RUnlock()

	req := withAdminContext(httptest.NewRequest("DELETE", "/api/v1/webhooks/0", nil))
	req.SetPathValue("id", "0")
	rec := httptest.NewRecorder()
	s.handleWebhookDelete(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete code=%d body=%s", rec.Code, rec.Body.String())
	}
	if snap[0].URL != "https://8.8.8.8/a" || snap[1].URL != "https://8.8.8.8/b" || snap[2].URL != "https://8.8.8.8/c" {
		t.Fatalf("reader snapshot rewritten: %+v", snap)
	}
	if got := s.config.Global.Webhooks; len(got) != 2 || got[0].URL != "https://8.8.8.8/b" || got[1].URL != "https://8.8.8.8/c" {
		t.Fatalf("live webhooks = %+v, want b,c", got)
	}
}

// TestWebhookCreateRejectsNegativeLimits pins that negative retry/timeout are
// rejected: a negative timeout makes every delivery's dial fail immediately.
func TestWebhookCreateRejectsNegativeLimits(t *testing.T) {
	for _, body := range []string{
		`{"url":"https://8.8.8.8/h","retry":-1}`,
		`{"url":"https://8.8.8.8/h","timeout":-5}`,
		`{"url":"https://8.8.8.8/h","timeout":"-1ns"}`,
	} {
		s := testServer()
		rec := httptest.NewRecorder()
		s.handleWebhookCreate(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/webhooks",
			strings.NewReader(body))))
		if rec.Code != 400 || len(s.config.Global.Webhooks) != 0 {
			t.Errorf("%s: code=%d stored=%d, want 400 and nothing stored", body, rec.Code, len(s.config.Global.Webhooks))
		}
	}
}
