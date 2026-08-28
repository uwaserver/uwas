package cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// global.cache.vary_by_query was dead configuration: GenerateKey always folded
// the query string in and nothing read the field, so an operator could not
// collapse /page?utm_source=a and /page?utm_source=b onto one entry however
// they set it.

func newRequest(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "cache.test"
	return r
}

func TestGenerateKeyWithoutQueryCollapsesQueries(t *testing.T) {
	a := GenerateKeyWithoutQuery(newRequest("/p?utm_source=a"), nil)
	b := GenerateKeyWithoutQuery(newRequest("/p?utm_source=b"), nil)
	if a != b {
		t.Errorf("the queries produced different keys:\n  %q\n  %q", a, b)
	}
	if plain := GenerateKeyWithoutQuery(newRequest("/p"), nil); a != plain {
		t.Errorf("a request with no query got a different key:\n  %q\n  %q", a, plain)
	}
	// Different paths must still be different.
	if other := GenerateKeyWithoutQuery(newRequest("/q?utm_source=a"), nil); a == other {
		t.Error("different paths collapsed onto one key")
	}
}

// The default must not change: the query has always been part of the key.
func TestGenerateKeyKeepsQueryByDefault(t *testing.T) {
	a := GenerateKey(newRequest("/p?q=cats"), nil)
	b := GenerateKey(newRequest("/p?q=dogs"), nil)
	if a == b {
		t.Fatal("the default key ignored the query — /p?q=cats and /p?q=dogs would share one entry")
	}
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	return NewEngine(context.Background(), 8<<20, "", 0, logger.New("error", "text"))
}

// liveEntry builds a live entry. Created must be set: a zero Created puts the
// expiry in year 1, the entry is never returned, and every "expected a miss"
// assertion below would pass without testing anything.
func liveEntry(body string) *CachedResponse {
	return &CachedResponse{
		StatusCode: 200,
		Body:       []byte(body),
		Created:    time.Now(),
		TTL:        time.Minute,
	}
}

// assertStored guards against exactly that: prove the entry is retrievable
// before asserting anything about a different request missing it.
func assertStored(t *testing.T, e *Engine, r *http.Request, want string) {
	t.Helper()
	got, _ := e.Get(r)
	if got == nil {
		t.Fatalf("%s was not cached — the test must not pass vacuously", r.URL)
	}
	if string(got.Body) != want {
		t.Fatalf("body = %q, want %q", got.Body, want)
	}
}

// A fresh engine must vary by query: absent config means the old behaviour.
func TestEngineVariesByQueryByDefault(t *testing.T) {
	e := testEngine(t)

	e.Set(newRequest("/p?q=cats"), liveEntry("cats"))
	assertStored(t, e, newRequest("/p?q=cats"), "cats")

	if got, _ := e.Get(newRequest("/p?q=dogs")); got != nil {
		t.Errorf("?q=dogs served ?q=cats' entry: %q", got.Body)
	}
}

func TestEngineCollapsesQueriesWhenDisabled(t *testing.T) {
	e := testEngine(t)
	e.SetVaryByQuery(false)

	e.Set(newRequest("/p?utm_source=a"), liveEntry("sayfa"))
	assertStored(t, e, newRequest("/p?utm_source=a"), "sayfa")

	got, _ := e.Get(newRequest("/p?utm_source=b"))
	if got == nil {
		t.Fatal("the queries stayed in separate entries with vary_by_query=false")
	}
	if string(got.Body) != "sayfa" {
		t.Errorf("body = %q", got.Body)
	}

	// Key() must agree with Get/Set, or the store path and the lookup path
	// would use different keys.
	if e.Key(newRequest("/p?utm_source=a")) != e.Key(newRequest("/p?utm_source=b")) {
		t.Error("Key() disagrees with Get/Set")
	}
}

func TestEngineDisabledStillSeparatesPaths(t *testing.T) {
	e := testEngine(t)
	e.SetVaryByQuery(false)

	e.Set(newRequest("/a?x=1"), liveEntry("a"))
	assertStored(t, e, newRequest("/a?x=1"), "a")

	if got, _ := e.Get(newRequest("/b?x=1")); got != nil {
		t.Errorf("/b served /a's entry: %q", got.Body)
	}
}

// Turning it back on must restore per-query entries.
func TestEngineVaryByQueryTogglesBack(t *testing.T) {
	e := testEngine(t)
	e.SetVaryByQuery(false)
	e.SetVaryByQuery(true)

	e.Set(newRequest("/p?q=cats"), liveEntry("cats"))
	assertStored(t, e, newRequest("/p?q=cats"), "cats")

	if got, _ := e.Get(newRequest("/p?q=dogs")); got != nil {
		t.Errorf("still collapsed after being turned back on: %q", got.Body)
	}
}
