package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestContainsESI(t *testing.T) {
	if !ContainsESI([]byte(`<html><!--esi <esi:include src="/nav" /> --></html>`)) {
		t.Error("should detect ESI markers")
	}
	if ContainsESI([]byte(`<html><body>Hello</body></html>`)) {
		t.Error("should not detect ESI in plain HTML")
	}
	if ContainsESI(nil) {
		t.Error("nil body should not contain ESI")
	}
}

// mockFetcher implements ESIFragmentFetcher for tests.
type mockFetcher struct {
	fragments map[string][]byte
}

func (m *mockFetcher) FetchFragment(host, path string, _ *http.Request) ([]byte, int, http.Header, error) {
	if body, ok := m.fragments[path]; ok {
		h := make(http.Header)
		h.Set("Cache-Control", "max-age=300")
		return body, 200, h, nil
	}
	return nil, 404, nil, nil
}

func newTestESIProcessor(fragments map[string][]byte) *ESIProcessor {
	engine := &Engine{memory: NewMemoryCache(1 << 20)}
	fetcher := &mockFetcher{fragments: fragments}
	return NewESIProcessor(engine, fetcher, nil, 3)
}

func TestESISingleInclude(t *testing.T) {
	p := newTestESIProcessor(map[string][]byte{
		"/nav": []byte(`<nav>Home | About</nav>`),
	})
	body := []byte(`<html><!--esi <esi:include src="/nav" /> --><body>Content</body></html>`)
	req := httptest.NewRequest("GET", "/", nil)

	result, err := p.Process(body, "example.com", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := `<html><nav>Home | About</nav><body>Content</body></html>`
	if string(result) != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestESIMultipleIncludes(t *testing.T) {
	p := newTestESIProcessor(map[string][]byte{
		"/header": []byte(`<header>HEADER</header>`),
		"/footer": []byte(`<footer>FOOTER</footer>`),
	})
	body := []byte(`<!--esi <esi:include src="/header" /> -->BODY<!--esi <esi:include src="/footer" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)

	result, err := p.Process(body, "example.com", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := `<header>HEADER</header>BODY<footer>FOOTER</footer>`
	if string(result) != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestESIRemoveBlock(t *testing.T) {
	p := newTestESIProcessor(map[string][]byte{
		"/nav": []byte(`<nav>Dynamic Nav</nav>`),
	})
	body := []byte(`<!--esi <esi:include src="/nav" /> --><esi:remove><nav>Fallback Nav</nav></esi:remove>`)
	req := httptest.NewRequest("GET", "/", nil)

	result, err := p.Process(body, "example.com", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := `<nav>Dynamic Nav</nav>`
	if string(result) != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestESIFragmentCaching(t *testing.T) {
	callCount := 0
	engine := &Engine{memory: NewMemoryCache(1 << 20)}
	fetcher := &mockFetcher{fragments: map[string][]byte{
		"/nav": []byte(`<nav>Cached</nav>`),
	}}
	// Wrap to count calls
	p := NewESIProcessor(engine, fetcher, nil, 3)

	body := []byte(`<!--esi <esi:include src="/nav" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)

	// First call: cache miss, fetches from fetcher
	p.Process(body, "test.com", req, nil, 0)

	// Fragment should now be cached
	cached, status := engine.GetByKey("esi|test.com|/nav")
	if cached == nil || status != StatusHit {
		t.Errorf("fragment not cached, status=%s", status)
	}

	// Second call: should use cache (fetcher not called again)
	_ = callCount
	result, _ := p.Process(body, "test.com", req, nil, 0)
	if string(result) != `<nav>Cached</nav>` {
		t.Errorf("got %q", result)
	}
}

func TestESIMaxDepth(t *testing.T) {
	// Fragment contains ESI — nested. Depth limit should stop recursion.
	p := newTestESIProcessor(map[string][]byte{
		"/level1": []byte(`L1<!--esi <esi:include src="/level2" /> -->`),
		"/level2": []byte(`L2<!--esi <esi:include src="/level3" /> -->`),
		"/level3": []byte(`L3<!--esi <esi:include src="/level4" /> -->`),
		"/level4": []byte(`L4`),
	})
	body := []byte(`<!--esi <esi:include src="/level1" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)

	// maxDepth=3: depth 0→1→2, depth 3 stops
	result, _ := p.Process(body, "test.com", req, nil, 0)
	rs := string(result)
	// Should include L1, L2, L3 but L3's include of /level4 should NOT be resolved
	if rs != `L1L2L3<!--esi <esi:include src="/level4" /> -->` {
		t.Errorf("unexpected result: %q", rs)
	}
}

func TestESIFetchError(t *testing.T) {
	// Fragment not found → graceful degradation
	p := newTestESIProcessor(map[string][]byte{})
	body := []byte(`Before<!--esi <esi:include src="/missing" /> -->After`)
	req := httptest.NewRequest("GET", "/", nil)

	result, err := p.Process(body, "test.com", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	rs := string(result)
	if rs != `Before<!-- ESI error: /missing -->After` {
		t.Errorf("got %q", rs)
	}
}

func TestESINoMarkers(t *testing.T) {
	p := newTestESIProcessor(nil)
	body := []byte(`<html><body>No ESI here</body></html>`)
	req := httptest.NewRequest("GET", "/", nil)

	result, _ := p.Process(body, "test.com", req, nil, 0)
	if string(result) != string(body) {
		t.Error("body should pass through unchanged")
	}
}

func TestESIFragmentKey(t *testing.T) {
	key := esiFragmentKey("example.com", "/nav")
	if key != "esi|example.com|/nav" {
		t.Errorf("key = %q", key)
	}
}

func TestParseFragmentTTL(t *testing.T) {
	h := make(http.Header)
	h.Set("Cache-Control", "public, max-age=600")
	ttl := parseFragmentTTL(h)
	if ttl != 600*time.Second {
		t.Errorf("ttl = %v, want 600s", ttl)
	}

	h2 := make(http.Header)
	ttl2 := parseFragmentTTL(h2)
	if ttl2 != 0 {
		t.Errorf("empty header should return 0, got %v", ttl2)
	}
}

func TestCachedResponseESITemplate(t *testing.T) {
	resp := &CachedResponse{
		StatusCode:  200,
		Headers:     make(http.Header),
		Body:        []byte("test"),
		Created:     time.Now(),
		TTL:         time.Minute,
		ESITemplate: true,
	}

	data := resp.Serialize()
	decoded, err := Deserialize(data)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.ESITemplate {
		t.Error("ESITemplate should be true after roundtrip")
	}

	// Test backward compat: old format without ESI byte
	resp2 := &CachedResponse{
		StatusCode: 200,
		Headers:    make(http.Header),
		Body:       []byte("old"),
		Created:    time.Now(),
		TTL:        time.Minute,
	}
	data2 := resp2.Serialize()
	// Strip the ESI byte to simulate old format
	data2Old := data2[:len(data2)-1]
	decoded2, err := Deserialize(data2Old)
	if err != nil {
		t.Fatal(err)
	}
	if decoded2.ESITemplate {
		t.Error("old format should default ESITemplate to false")
	}
}
