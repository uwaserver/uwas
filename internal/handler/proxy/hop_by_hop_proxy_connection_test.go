package proxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

// "Proxy-Connection" is a legacy hop-by-hop header (RFC 9110 §7.6.1
// note; Go's httputil.ReverseProxy strips it) and the shared list also spells
// "Trailer" as "Trailers" (Go source cites errata 4522). Neither request nor
// response direction removed it (F2321).
func rawUpstreamHead(t *testing.T, respHeaders string) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var sb strings.Builder
		for {
			line, err := br.ReadString('\n')
			sb.WriteString(line)
			if err != nil || line == "\r\n" {
				break
			}
		}
		got <- sb.String()
		fmt.Fprintf(c, "HTTP/1.1 200 OK\r\n%sContent-Length: 0\r\nConnection: close\r\n\r\n", respHeaders)
	}()
	return "http://" + ln.Addr().String(), got
}

func TestProxyDropsProxyConnectionBothWays(t *testing.T) {
	addr, got := rawUpstreamHead(t, "Proxy-Connection: keep-alive\r\nX-Up: 1\r\n")
	pool := NewUpstreamPool([]UpstreamConfig{{Address: addr, Weight: 1}})
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("X-Control", "keep")
	req.Header.Set("Proxy-Connection", "keep-alive")
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	New(newTestLogger()).Serve(ctx, newTestDomain(), pool, NewBalancer("round_robin"))
	head := <-got

	reqHas := strings.Contains(strings.ToLower(head), "proxy-connection:")
	ctl := strings.Contains(head, "X-Control: keep")
	respHas := rec.Header().Get("Proxy-Connection") != ""
	if !ctl || rec.Header().Get("X-Up") != "1" || rec.Code != http.StatusOK {
		t.Fatal("invalid proof: control failed")
	}
	if reqHas || respHas {
		t.Fatalf("Proxy-Connection forwarded: upstream=%v client=%v", reqHas, respHas)
	}
}

func TestRemoveHopByHopTrailerAndProxyConnection(t *testing.T) {
	h := http.Header{}
	for _, k := range []string{"Trailer", "Proxy-Connection", "Te", "Keep-Alive", "Upgrade", "Transfer-Encoding"} {
		h.Set(k, "x")
	}
	h.Set("X-Keep", "1")
	removeHopByHop(h)
	if len(h) != 1 || h.Get("X-Keep") != "1" {
		t.Fatalf("left headers = %v, want only X-Keep", h)
	}
}
