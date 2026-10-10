package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func compressTestGet(t *testing.T, h http.Handler, ae string) (*http.Response, []byte) {
	t.Helper()
	srv := httptest.NewServer(CompressWith(1024, nil)(h))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Accept-Encoding", ae)
	tr := &http.Transport{DisableCompression: true}
	t.Cleanup(tr.CloseIdleConnections)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// A compressed response must carry the Content-Type net/http would have
// sniffed for the identity response: net/http skips sniffing once
// Content-Encoding is set, so the middleware has to do it.
func TestCompressSniffsMissingContentType(t *testing.T) {
	body := []byte("<!DOCTYPE html><html><body>" + strings.Repeat("hello world ", 300) + "</body></html>")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	resp, _ := compressTestGet(t, h, "gzip")
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", resp.Header.Get("Content-Encoding"))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want sniffed text/html", ct)
	}
}

// A Flush before the first Write commits the headers; the body that follows
// must not be gzip bytes sent without a Content-Encoding header.
func TestCompressFlushBeforeWriteKeepsBodyDecodable(t *testing.T) {
	text := bytes.Repeat([]byte("data: event payload line\n"), 100)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		w.Write(text)
	})
	resp, raw := compressTestGet(t, h, "gzip")
	got := raw
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		got, _ = io.ReadAll(gr)
	}
	if !bytes.Equal(got, text) {
		t.Fatalf("body does not decode per headers (Content-Encoding=%q, first bytes % x)",
			resp.Header.Get("Content-Encoding"), raw[:min(2, len(raw))])
	}
}

// The q parameter name is case-insensitive, so "gzip;Q=0" is a refusal.
func TestSelectEncodingUpperCaseQZero(t *testing.T) {
	for _, ae := range []string{"gzip;Q=0", "GZIP;Q=0.0", "br;Q=0, gzip;Q=0"} {
		if got := selectEncoding(ae); got != encodingNone {
			t.Errorf("selectEncoding(%q) = %d, want none", ae, got)
		}
	}
	if got := selectEncoding("br;Q=0, gzip"); got != encodingGzip {
		t.Errorf("selectEncoding(br;Q=0, gzip) = %d, want gzip", got)
	}
}
