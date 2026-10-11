package pathmatch

import "testing"

// F2531: cache-rule regexes match the canonical path, like locations.
func TestRegexMatchesCanonicalPath(t *testing.T) {
	for _, p := range []string{"/cart/x", "//cart/x", "/./cart/x", "/a/../cart/x", "/cart/"} {
		if !Regex(p, `^/cart`) {
			t.Errorf("Regex(%q, ^/cart) = false, want true", p)
		}
	}
	for _, p := range []string{"/carts-not", "/shop/cart", "/", ""} {
		want := p == "/carts-not"
		if got := Regex(p, `^/cart`); got != want {
			t.Errorf("Regex(%q, ^/cart) = %v, want %v", p, got, want)
		}
	}
	// A trailing slash is kept, so a pattern anchored on it still matches.
	if !Regex("//dir//", `/$`) {
		t.Error(`Regex("//dir//", /$) = false, want true`)
	}
	// Unaffected: extension rules and an uncompilable pattern.
	if !Regex("/a/b.css", `\.css$`) || Regex("/a", `([`) {
		t.Error("extension/invalid-pattern behaviour changed")
	}
}
