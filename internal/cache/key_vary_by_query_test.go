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

func istek(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "cache.test"
	return r
}

func TestGenerateKeyWithoutQueryCollapsesQueries(t *testing.T) {
	a := GenerateKeyWithoutQuery(istek("/p?utm_source=a"), nil)
	b := GenerateKeyWithoutQuery(istek("/p?utm_source=b"), nil)
	if a != b {
		t.Errorf("sorgular ayrı anahtar üretti:\n  %q\n  %q", a, b)
	}
	if plain := GenerateKeyWithoutQuery(istek("/p"), nil); a != plain {
		t.Errorf("sorgusuz istek farklı anahtar aldı:\n  %q\n  %q", a, plain)
	}
	// Different paths must still be different.
	if other := GenerateKeyWithoutQuery(istek("/q?utm_source=a"), nil); a == other {
		t.Error("farklı yollar aynı anahtara çöktü")
	}
}

// The default must not change: the query has always been part of the key.
func TestGenerateKeyKeepsQueryByDefault(t *testing.T) {
	a := GenerateKey(istek("/p?q=cats"), nil)
	b := GenerateKey(istek("/p?q=dogs"), nil)
	if a == b {
		t.Fatal("varsayılan anahtar sorguyu yok saydı — /p?q=kedi ile /p?q=köpek aynı girdiyi paylaşır")
	}
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	return NewEngine(context.Background(), 8<<20, "", 0, logger.New("error", "text"))
}

// girdi builds a live entry. Created must be set: a zero Created puts the
// expiry in year 1, the entry is never returned, and every "expected a miss"
// assertion below would pass without testing anything.
func girdi(body string) *CachedResponse {
	return &CachedResponse{
		StatusCode: 200,
		Body:       []byte(body),
		Created:    time.Now(),
		TTL:        time.Minute,
	}
}

// saklandiMi guards against exactly that: prove the entry is retrievable
// before asserting anything about a different request missing it.
func saklandiMi(t *testing.T, e *Engine, r *http.Request, want string) {
	t.Helper()
	got, _ := e.Get(r)
	if got == nil {
		t.Fatalf("%s önbelleğe yazılmadı — test boşa geçemez", r.URL)
	}
	if string(got.Body) != want {
		t.Fatalf("gövde = %q, want %q", got.Body, want)
	}
}

// A fresh engine must vary by query: absent config means the old behaviour.
func TestEngineVariesByQueryByDefault(t *testing.T) {
	e := testEngine(t)

	e.Set(istek("/p?q=cats"), girdi("cats"))
	saklandiMi(t, e, istek("/p?q=cats"), "cats")

	if got, _ := e.Get(istek("/p?q=dogs")); got != nil {
		t.Errorf("?q=dogs, ?q=cats'in girdisini aldı: %q", got.Body)
	}
}

func TestEngineCollapsesQueriesWhenDisabled(t *testing.T) {
	e := testEngine(t)
	e.SetVaryByQuery(false)

	e.Set(istek("/p?utm_source=a"), girdi("sayfa"))
	saklandiMi(t, e, istek("/p?utm_source=a"), "sayfa")

	got, _ := e.Get(istek("/p?utm_source=b"))
	if got == nil {
		t.Fatal("vary_by_query=false iken sorgular ayrı girdilerde kaldı")
	}
	if string(got.Body) != "sayfa" {
		t.Errorf("gövde = %q", got.Body)
	}

	// Key() must agree with Get/Set, or the store path and the lookup path
	// would use different keys.
	if e.Key(istek("/p?utm_source=a")) != e.Key(istek("/p?utm_source=b")) {
		t.Error("Key() Get/Set ile aynı fikirde değil")
	}
}

func TestEngineDisabledStillSeparatesPaths(t *testing.T) {
	e := testEngine(t)
	e.SetVaryByQuery(false)

	e.Set(istek("/a?x=1"), girdi("a"))
	saklandiMi(t, e, istek("/a?x=1"), "a")

	if got, _ := e.Get(istek("/b?x=1")); got != nil {
		t.Errorf("/b, /a'nın girdisini aldı: %q", got.Body)
	}
}

// Turning it back on must restore per-query entries.
func TestEngineVaryByQueryTogglesBack(t *testing.T) {
	e := testEngine(t)
	e.SetVaryByQuery(false)
	e.SetVaryByQuery(true)

	e.Set(istek("/p?q=cats"), girdi("cats"))
	saklandiMi(t, e, istek("/p?q=cats"), "cats")

	if got, _ := e.Get(istek("/p?q=dogs")); got != nil {
		t.Errorf("tekrar açıldıktan sonra da çöktü: %q", got.Body)
	}
}
