package cache

import (
	"net/http/httptest"
	"testing"
)

// TestGenerateKeyPathQueryDelimiterCollision is a regression test for a
// cross-URL cache-key collision.
//
// generateKey used to build the key as method|scheme|host|path|query|vary
// using '|' delimiters, but components can legally contain '|': r.URL.Path
// is the DECODED path (%7C decodes to '|'), r.URL.RawQuery preserves raw
// '|', and vary-header values may contain it. Two distinct URLs produced
// byte-identical keys:
//
//	GET http://h/p?x|   → GET|http|h|/p|x|   (path /p, raw query "x|")
//	GET http://h/p%7Cx  → GET|http|h|/p|x|   (path decodes to /p|x, no query)
//
// A shared key meant the cache served one URL's response body for the
// other. generateKey now length-prefixes every component, so boundaries are
// determined by parsed lengths and no component content can forge a key.
func TestGenerateKeyPathQueryDelimiterCollision(t *testing.T) {
	a := httptest.NewRequest("GET", "http://h/p?x|", nil)  // query contains '|'
	b := httptest.NewRequest("GET", "http://h/p%7Cx", nil) // path decodes to /p|x

	ka := GenerateKey(a, nil)
	kb := GenerateKey(b, nil)

	if ka == kb {
		t.Fatalf("distinct requests collided on cache key %q — the cache serves one URL's content for the other", ka)
	}
}

// Control: query parameter ORDER must not change the key — the documented
// canonicalization (key=a&b and key=b&a → same key) must keep working.
func TestGenerateQueryParamOrderStillCanonicalized(t *testing.T) {
	a := httptest.NewRequest("GET", "http://h/p?a=1&b=2", nil)
	b := httptest.NewRequest("GET", "http://h/p?b=2&a=1", nil)
	if GenerateKey(a, nil) != GenerateKey(b, nil) {
		t.Fatal("reordered params must share one key")
	}
}

// Control: genuinely different paths and vary values must keep distinct keys.
func TestGenerateDistinctRequestsDistinctKeys(t *testing.T) {
	a := httptest.NewRequest("GET", "http://h/p", nil)
	b := httptest.NewRequest("GET", "http://h/q", nil)
	if GenerateKey(a, nil) == GenerateKey(b, nil) {
		t.Fatal("different paths must have different keys")
	}

	c := httptest.NewRequest("GET", "http://h/p", nil)
	c.Header.Set("Accept-Encoding", "gzip")
	d := httptest.NewRequest("GET", "http://h/p", nil)
	d.Header.Set("Accept-Encoding", "br")
	if GenerateKey(c, []string{"Accept-Encoding"}) == GenerateKey(d, []string{"Accept-Encoding"}) {
		t.Fatal("different vary values must have different keys")
	}
}
