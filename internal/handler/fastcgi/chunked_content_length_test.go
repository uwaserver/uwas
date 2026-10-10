package fastcgi

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/fastcgi"
)

// F1840: a chunked request body has no Content-Length, and PHP-FPM sizes the
// body from CONTENT_LENGTH, so PHP read an empty body.

// captureFCGI serves one request, returning its CONTENT_LENGTH param and stdin.
func captureFCGI(t *testing.T, ln net.Listener) <-chan [2]string {
	out := make(chan [2]string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			out <- [2]string{"accept-error", ""}
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		bw := bufio.NewWriter(c)
		var params, stdin bytes.Buffer
		for {
			rec, err := fastcgi.ReadRecord(br)
			if err != nil {
				out <- [2]string{"read-error", ""}
				return
			}
			switch rec.Type {
			case fastcgi.TypeParams:
				params.Write(rec.Content)
			case fastcgi.TypeStdin:
				if rec.ContentLength == 0 {
					p, _ := fastcgi.DecodeParams(params.Bytes())
					fastcgi.WriteRecord(bw, fastcgi.TypeStdout, 1, []byte("Status: 200 OK\r\nContent-Type: text/plain\r\n\r\nok"))
					fastcgi.WriteRecord(bw, fastcgi.TypeStdout, 1, nil)
					end := make([]byte, 8)
					binary.BigEndian.PutUint32(end[0:4], 0)
					fastcgi.WriteRecord(bw, fastcgi.TypeEndRequest, 1, end)
					bw.Flush()
					out <- [2]string{p["CONTENT_LENGTH"], stdin.String()}
					return
				}
				stdin.Write(rec.Content)
			}
		}
	}()
	return out
}

func serveBody(t *testing.T, chunked bool, body string) (string, string) {
	cl, in, _ := serveBodyWith(t, chunked, body, 0)
	return cl, in
}

func serveBodyWith(t *testing.T, chunked bool, body string, maxUpload config.ByteSize) (string, string, int) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	out := captureFCGI(t, ln)
	h := New(logger.New("error", "text"))
	h.clients.Store(ln.Addr().String(), fastcgi.NewClient(fastcgi.PoolConfig{Address: "tcp:" + ln.Addr().String(), MaxIdle: 1, MaxOpen: 1}))
	domain := &config.Domain{Host: "php.test", Root: "/var/www", Type: "php", PHP: config.PHPConfig{FPMAddress: ln.Addr().String(), IndexFiles: []string{"index.php"}, MaxUpload: maxUpload}}
	req := httptest.NewRequest("POST", "/api.php", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if chunked {
		// What net/http's server hands a handler for a chunked upload: no
		// Content-Length header, ContentLength -1, TransferEncoding set.
		req.Header.Del("Content-Length")
		req.ContentLength = -1
		req.TransferEncoding = []string{"chunked"}
	} else {
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Length", fmt.Sprint(len(body)))
	}
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/api.php"
	ctx.OriginalURI = "/api.php"
	h.Serve(ctx, domain)
	if rec.Code == 413 || rec.Code == 400 {
		ln.Close() // the FastCGI side was never contacted
		return "", "", rec.Code
	}
	r := <-out
	return r[0], r[1], rec.Code
}

func TestChunkedBodyGetsContentLength(t *testing.T) {
	const body = "a=1&b=2"
	if cl, in := serveBody(t, false, body); cl != "7" || in != body {
		t.Fatalf("known length: CONTENT_LENGTH=%q stdin=%q", cl, in)
	}
	if cl, in := serveBody(t, true, body); cl != "7" || in != body {
		t.Errorf("chunked: CONTENT_LENGTH=%q stdin=%q, want 7 and the body", cl, in)
	}
	if cl, in := serveBody(t, true, ""); cl != "0" || in != "" {
		t.Errorf("empty chunked: CONTENT_LENGTH=%q stdin=%q, want 0", cl, in)
	}
}

func TestChunkedBodyOverLimitRejected(t *testing.T) {
	// MaxUpload 1 MiB + 1 MiB post_max headroom = 2 MiB of buffering.
	big := strings.Repeat("x", 2<<20+1)
	if _, _, code := serveBodyWith(t, true, big, config.MB); code != 413 {
		t.Errorf("oversized chunked body: status %d, want 413", code)
	}
	ok := strings.Repeat("x", 2<<20)
	if cl, _, code := serveBodyWith(t, true, ok, config.MB); code != 200 || cl != "2097152" {
		t.Errorf("body at the limit: status %d CONTENT_LENGTH=%q, want 200/2097152", code, cl)
	}
}
