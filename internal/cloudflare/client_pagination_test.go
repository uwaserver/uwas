package cloudflare

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// openBodyTracker wraps a RoundTripper and records the highest number of
// response bodies that are simultaneously open. doListPages reads each page
// before requesting the next one, so at most one body should ever be live.
type openBodyTracker struct {
	inner http.RoundTripper
	mu    sync.Mutex
	open  int
	peak  int
}

type trackedBody struct {
	io.ReadCloser
	tr *openBodyTracker
	mu sync.Mutex
}

func (b *trackedBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tr.mu.Lock()
	b.tr.open--
	b.tr.mu.Unlock()
	return b.ReadCloser.Close()
}

func (t *openBodyTracker) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.open++
	if t.open > t.peak {
		t.peak = t.open
	}
	t.mu.Unlock()
	resp.Body = &trackedBody{ReadCloser: resp.Body, tr: t}
	return resp, nil
}

func (t *openBodyTracker) peakOpen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.peak
}

// newPagedTestClient serves totalPages pages of a Cloudflare list envelope and
// returns a client wired to it plus the tracker and a page-request counter.
func newPagedTestClient(t *testing.T, totalPages int, failOnPage int) (*Client, *openBodyTracker, func() int) {
	t.Helper()

	mu := sync.Mutex{}
	served := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
		}
		mu.Lock()
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if failOnPage > 0 && page == failOnPage {
			fmt.Fprint(w, `{"success":false,"errors":[{"code":1000,"message":"page failure"}]}`)
			return
		}
		fmt.Fprintf(w,
			`{"success":true,"result":[{"name":"z%d.example.com","id":"id%d"}],"result_info":{"total_pages":%d}}`,
			page, page, totalPages)
	}))
	t.Cleanup(srv.Close)

	tr := &openBodyTracker{inner: http.DefaultTransport}
	c := New("token", "account")
	c.baseURL = srv.URL
	c.http = &http.Client{Transport: tr}
	return c, tr, func() int { mu.Lock(); defer mu.Unlock(); return served }
}

// TestDoListPagesClosesEachPageBeforeRequestingNext is the regression for a
// `defer resp.Body.Close()` placed inside doListPages' per-page loop: Go runs
// defer at function return, so every page's body and pooled connection stayed
// open for the entire pagination walk. An account with many pages — exactly the
// case doListPages exists to serve — held one live body per page.
func TestDoListPagesClosesEachPageBeforeRequestingNext(t *testing.T) {
	const totalPages = 12

	c, tr, served := newPagedTestClient(t, totalPages, 0)

	pages, err := c.doListPages("/zones")
	if err != nil {
		t.Fatalf("doListPages: %v", err)
	}
	if got := served(); got != totalPages {
		t.Fatalf("served %d pages, want %d — harness did not exercise pagination", got, totalPages)
	}
	if len(pages) != totalPages {
		t.Fatalf("got %d result pages, want %d", len(pages), totalPages)
	}
	if peak := tr.peakOpen(); peak > 1 {
		t.Fatalf("held %d response bodies open at once across %d pages; want at most 1", peak, totalPages)
	}
}

// Boundary: a single-page list must still be collected (the "already fits on
// one page" case that the loop never enters).
func TestDoListPagesSinglePageStillCollected(t *testing.T) {
	c, tr, _ := newPagedTestClient(t, 1, 0)

	pages, err := c.doListPages("/zones")
	if err != nil {
		t.Fatalf("doListPages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	if peak := tr.peakOpen(); peak > 1 {
		t.Fatalf("peak open bodies = %d, want at most 1", peak)
	}
}

// Secondary branch the fix touched: an API error on a later page must still be
// returned to the caller rather than swallowed by the reworked read/close.
func TestDoListPagesLaterPageErrorPropagates(t *testing.T) {
	c, _, _ := newPagedTestClient(t, 3, 2)

	if _, err := c.doListPages("/zones"); err == nil {
		t.Fatal("expected an error when page 2 reports success:false, got nil")
	}
}
