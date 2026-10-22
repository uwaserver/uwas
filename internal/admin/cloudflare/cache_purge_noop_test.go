package cloudflare

// Regression test for F24601: a purge request with neither "url" nor
// "everything" listed the zones, purged nothing and answered
// {"status":"purged"} with a success audit entry.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type purgeRT func(*http.Request) (*http.Response, error)

func (f purgeRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCachePurgeRejectsEmptyRequest(t *testing.T) {
	for body, want := range map[string]int{
		`{"url":"https://example.com/a.css"}`: http.StatusOK,
		`{"everything":true}`:                 http.StatusOK,
		`{}`:                                  http.StatusBadRequest,
		`{"url":""}`:                          http.StatusBadRequest,
		`{"everything":false}`:                http.StatusBadRequest,
	} {
		d := newTSDeps(&State{Connected: true, Token: "t", AccountID: "a"})
		rec := httptest.NewRecorder()
		New(d).CachePurge(rec, tsReq(http.MethodPost, body, ""))
		if rec.Code != want {
			t.Errorf("body %s: status %d, want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
}

func TestPurgeCacheWithClientEmptyRequestDoesNoCalls(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: purgeRT(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
			`{"success":true,"result":[{"id":"z1","name":"example.com"}],"result_info":{"total_pages":1}}`))}, nil
	})}
	if err := PurgeCacheWithClient(client, "t", "", false); err == nil {
		t.Fatal("empty purge must return an error")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("empty purge made %d API calls, want 0", n)
	}
	// control: a real purge still reaches the API
	if err := PurgeCacheWithClient(client, "t", "https://example.com/a.css", false); err != nil {
		t.Fatalf("url purge: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("url purge made no API calls")
	}
}
