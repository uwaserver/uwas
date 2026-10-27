package server

// F36301: a response served from the server cache did not behave like the
// same response served fresh. The hit path wrote the stored body with
// WriteHeader/Write only, so over a real connection it
//   - went out chunked with no Content-Length (the length is stripped when the
//     entry is stored and was never restored), where the fresh response of the
//     same URL carries one;
//   - ignored Range and sent the whole body as 200 while still advertising
//     "Accept-Ranges: bytes" (media players seeking in a cached video, and
//     Safari in particular, require 206);
//   - ignored If-Modified-Since, answering 200 with the full body for a client
//     whose copy is current (only If-None-Match was evaluated).

import (
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheHitHonoursLengthRangeAndConditionals(t *testing.T) {
	root := t.TempDir()
	// Random bytes do not compress, so the compress middleware leaves the
	// body alone and the length on the wire is the stored body's.
	data := make([]byte, 100_000)
	rand.New(rand.NewSource(1)).Read(data)
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newStaticCacheChain(t, root))
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	get := func(hdr map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/blob.bin", nil)
		req.Host = "cache.test"
		req.Header.Set("User-Agent", "uwas-test")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		return resp, body
	}

	// Control: the first request is a MISS served by the static handler.
	miss, body := get(nil)
	if miss.StatusCode != 200 || miss.ContentLength != int64(len(data)) || len(body) != len(data) {
		t.Fatalf("control (MISS): status=%d CL=%d body=%d", miss.StatusCode, miss.ContentLength, len(body))
	}
	lastModified := miss.Header.Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("control: no Last-Modified on the fresh response")
	}

	t.Run("full hit has a Content-Length", func(t *testing.T) {
		resp, body := get(nil)
		if resp.Header.Get("X-Cache") != "HIT" {
			t.Fatalf("X-Cache = %q, want HIT", resp.Header.Get("X-Cache"))
		}
		t.Logf("EXPECTED: Content-Length=%d\nACTUAL: Content-Length=%d TE=%v", len(data), resp.ContentLength, resp.TransferEncoding)
		if resp.StatusCode != 200 || len(body) != len(data) {
			t.Fatalf("status=%d body=%d", resp.StatusCode, len(body))
		}
		if resp.ContentLength != int64(len(data)) {
			t.Fatalf("PROBLEM CONFIRMED: hit Content-Length = %d (TE %v), want %d", resp.ContentLength, resp.TransferEncoding, len(data))
		}
	})

	t.Run("Range is answered with 206", func(t *testing.T) {
		resp, body := get(map[string]string{"Range": "bytes=10-19"})
		if resp.Header.Get("X-Cache") != "HIT" {
			t.Fatalf("X-Cache = %q, want HIT", resp.Header.Get("X-Cache"))
		}
		t.Logf("EXPECTED: 206 bytes 10-19/%d, 10 bytes\nACTUAL: %d %q, %d bytes", len(data), resp.StatusCode, resp.Header.Get("Content-Range"), len(body))
		if resp.StatusCode != http.StatusPartialContent {
			t.Fatalf("PROBLEM CONFIRMED: status = %d, want 206", resp.StatusCode)
		}
		if got, want := resp.Header.Get("Content-Range"), "bytes 10-19/100000"; got != want {
			t.Fatalf("Content-Range = %q, want %q", got, want)
		}
		if string(body) != string(data[10:20]) {
			t.Fatalf("partial body differs from the source bytes")
		}
	})

	t.Run("unsatisfiable Range is 416", func(t *testing.T) {
		resp, _ := get(map[string]string{"Range": "bytes=900000-"})
		if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("PROBLEM CONFIRMED: status = %d, want 416", resp.StatusCode)
		}
	})

	t.Run("If-Modified-Since for a current copy is 304", func(t *testing.T) {
		resp, body := get(map[string]string{"If-Modified-Since": lastModified})
		if resp.Header.Get("X-Cache") != "HIT" {
			t.Fatalf("X-Cache = %q, want HIT", resp.Header.Get("X-Cache"))
		}
		if resp.StatusCode != http.StatusNotModified || len(body) != 0 {
			t.Fatalf("PROBLEM CONFIRMED: status=%d body=%d, want 304 with no body", resp.StatusCode, len(body))
		}
	})

	t.Run("If-Modified-Since older than the file gets the body", func(t *testing.T) {
		resp, body := get(map[string]string{"If-Modified-Since": "Mon, 02 Jan 2006 15:04:05 GMT"})
		if resp.StatusCode != 200 || len(body) != len(data) {
			t.Fatalf("status=%d body=%d, want 200 with the full body", resp.StatusCode, len(body))
		}
	})

	t.Run("If-None-Match still answers 304", func(t *testing.T) {
		resp, body := get(map[string]string{"If-None-Match": miss.Header.Get("Etag")})
		if resp.StatusCode != http.StatusNotModified || len(body) != 0 {
			t.Fatalf("status=%d body=%d, want 304 with no body", resp.StatusCode, len(body))
		}
	})
}
