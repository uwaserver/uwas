package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/testport"
)

// F9000: middleware.Recovery aborts a response that already started by
// re-panicking http.ErrAbortHandler (F8800). On port 80 that panic reaches
// handleHTTP's own recover(); it must pass the abort on rather than append an
// error page to the partial body and end it as if complete.
func TestHandleHTTPKeepsAbortOfStartedResponse(t *testing.T) {
	addr := testport.FreeAddr(t)

	log := logger.New("error", "text")
	cfg := &config.Config{
		Global:  config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text", HTTPListen: addr},
		Domains: []config.Domain{{Host: "a.test", Type: "static", Root: t.TempDir(), SSL: config.SSLConfig{Mode: "off"}}},
	}
	s := New(cfg, log)
	t.Cleanup(func() { s.cancel(); time.Sleep(50 * time.Millisecond) })
	s.handler = middleware.Recovery(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/before" {
			panic("boom before any write")
		}
		fmt.Fprint(w, "PARTIAL-BODY-")
		w.(http.Flusher).Flush()
		panic("boom after partial write")
	}))
	if err := s.startHTTP(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.httpSrv.Close() })

	get := func(path string) (int, string, error) {
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		req.Host = "a.test"
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		b, rerr := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), rerr
	}

	// A panic before anything is sent is still answered with a clean 500.
	if st, body, err := get("/before"); st != 500 || err != nil || !strings.Contains(body, "500 Internal Server Error") {
		t.Fatalf("panic before any write: status=%d body=%q err=%v, want a clean 500", st, body, err)
	}

	// A panic after the response started must abort the transfer.
	st, body, err := get("/after")
	if err == nil {
		t.Errorf("started response ended as complete: status=%d body=%q, want a failed transfer", st, body)
	}
	if strings.Contains(body, "500 Internal Server Error") {
		t.Errorf("error page appended to a partial body: %q", body)
	}
}
