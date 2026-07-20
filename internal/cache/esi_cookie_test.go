package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// countingFetcher counts sub-request fetches per path.
type countingFetcher struct {
	fragments map[string][]byte
	calls     map[string]int
}

func (m *countingFetcher) FetchFragment(host, path string, _ *http.Request) ([]byte, int, http.Header, error) {
	m.calls[path]++
	if body, ok := m.fragments[path]; ok {
		h := make(http.Header)
		h.Set("Cache-Control", "max-age=300")
		return body, 200, h, nil
	}
	return nil, 404, nil, nil
}

// TestESIFragmentNotSharedAcrossCookies: fragments rendered for a
// cookie-carrying request must not be stored in (or served from) the
// cookie-blind shared fragment cache.
func TestESIFragmentNotSharedAcrossCookies(t *testing.T) {
	fetcher := &countingFetcher{
		fragments: map[string][]byte{"/widget": []byte(`<div>user data</div>`)},
		calls:     map[string]int{},
	}
	engine := &Engine{memory: NewMemoryCache(1 << 20)}
	p := NewESIProcessor(engine, fetcher, nil, 3)

	body := []byte(`<html><!--esi <esi:include src="/widget" /> --></html>`)

	// Logged-in user (cookie) triggers assembly first.
	authedReq := httptest.NewRequest("GET", "/", nil)
	authedReq.Header.Set("Cookie", "session=user-a")
	if _, err := p.Process(body, "example.com", authedReq, nil, 0); err != nil {
		t.Fatal(err)
	}

	// The fragment must NOT have been cached under the shared key.
	if cached, _ := engine.GetByKey(esiFragmentKey("example.com", "/widget")); cached != nil {
		t.Fatal("fragment rendered with cookies must not enter the shared cache")
	}

	// A second cookie-carrying request must re-fetch, not hit a cache.
	authedReq2 := httptest.NewRequest("GET", "/", nil)
	authedReq2.Header.Set("Cookie", "session=user-b")
	if _, err := p.Process(body, "example.com", authedReq2, nil, 0); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls["/widget"] != 2 {
		t.Fatalf("cookie requests must bypass fragment cache, got %d fetches", fetcher.calls["/widget"])
	}

	// Anonymous request populates the shared cache; a second one hits it.
	anonReq := httptest.NewRequest("GET", "/", nil)
	if _, err := p.Process(body, "example.com", anonReq, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Process(body, "example.com", anonReq, nil, 0); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls["/widget"] != 3 {
		t.Fatalf("anonymous fragment should be cached after first fetch, got %d fetches", fetcher.calls["/widget"])
	}
}
