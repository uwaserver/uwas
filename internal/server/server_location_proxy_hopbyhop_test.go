package server

// The location proxy's hop-by-hop filter used to check only a static header
// list (isHopByHopHeader), ignoring fields NAMED by Connection (RFC 9110
// §7.6.1): a client could send "Connection: X-Smuggled" plus "X-Smuggled:
// leak" and X-Smuggled reached the operator's upstream — the same laundering
// contract the domain proxy's removeHopByHop already implements. Both
// directions must launder: request headers toward the upstream, and response
// headers (named by the upstream's Connection) toward the client.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestLocationProxyStripsConnectionNamedFields(t *testing.T) {
	var mu sync.Mutex
	var gotReqHeader http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotReqHeader = r.Header.Clone()
		mu.Unlock()
		// The upstream names its own hop-by-hop field in its Connection
		// header, exercising the response direction.
		w.Header().Set("Connection", "X-Resp-Leak")
		w.Header().Set("X-Resp-Leak", "1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied"))
	}))
	t.Cleanup(up.Close)

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:      "loc.test",
			Type:      "static",
			Root:      t.TempDir(),
			SSL:       config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{{Match: "/p/", ProxyPass: up.URL}},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	h := s.buildMiddlewareChain()

	req := httptest.NewRequest(http.MethodGet, "/p/x", nil)
	req.Host = "loc.test"
	req.Header.Set("User-Agent", "uwas-test")
	req.Header.Set("Connection", "X-Smuggled")
	req.Header.Set("X-Smuggled", "leak")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	mu.Lock()
	defer mu.Unlock()
	if gotReqHeader == nil {
		t.Fatalf("upstream never received the request — rec.Code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := gotReqHeader.Get("X-Smuggled"); got != "" {
		t.Errorf("X-Smuggled = %q at the upstream — a Connection-named header leaked through the location proxy", got)
	}
	if got := rec.Header().Get("X-Resp-Leak"); got != "" {
		t.Errorf("X-Resp-Leak = %q at the client — an upstream Connection-named header leaked through the location proxy", got)
	}
	if got := rec.Header().Get("Connection"); got != "" {
		t.Errorf("Connection = %q at the client — hop-by-hop header forwarded to the client", got)
	}
}
