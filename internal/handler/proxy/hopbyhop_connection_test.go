package proxy

// Regression: removeHopByHop deleted the eight static hop-by-hop headers but
// never parsed the Connection header. Per RFC 7230 §6.1, "Connection" may name
// ADDITIONAL headers that are hop-by-hop for that connection, and those must be
// dropped before the request is forwarded. Without this, a client chooses which
// header gets laundered to the upstream by naming it in Connection.
//
// The pre-existing TestRemoveHopByHop used Connection: keep-alive — a name that
// is already in the static list — so it could not distinguish either behaviour.

import (
	"net/http"
	"testing"
)

func TestRemoveHopByHopDropsConnectionNamedHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "X-Secret, X-Another")
	h.Set("X-Secret", "internal-only-value")
	h.Set("X-Another", "also-internal")

	removeHopByHop(h)

	if got := h.Get("X-Secret"); got != "" {
		t.Errorf("Connection named X-Secret but it survived as %q; RFC 7230 §6.1 makes it hop-by-hop", got)
	}
	if got := h.Get("X-Another"); got != "" {
		t.Errorf("second Connection-named header X-Another survived as %q; comma-separated names must all be dropped", got)
	}
}

func TestRemoveHopByHopConnectionNamesAreCaseInsensitive(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "x-secret")
	h.Set("X-Secret", "drop-me")

	removeHopByHop(h)

	if got := h.Get("X-Secret"); got != "" {
		t.Errorf("Connection named 'x-secret' (lowercase) but X-Secret survived as %q; field names are case-insensitive", got)
	}
}

func TestRemoveHopByHopKeepsUnnamedHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "X-Secret")
	h.Set("X-Secret", "drop-me")
	h.Set("X-Ordinary", "keep-me")
	h.Set("Content-Type", "application/json")

	removeHopByHop(h)

	if got := h.Get("X-Ordinary"); got != "keep-me" {
		t.Errorf("X-Ordinary must survive, got %q", got)
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type must survive, got %q", got)
	}
}

func TestRemoveHopByHopStillDropsStaticList(t *testing.T) {
	h := http.Header{}
	for _, k := range hopByHopHeaders {
		h.Set(k, "value")
	}
	h.Set("X-Custom", "keep-me")

	removeHopByHop(h)

	for _, k := range hopByHopHeaders {
		if v := h.Get(k); v != "" {
			t.Errorf("static hop-by-hop header %s survived with %q", k, v)
		}
	}
	if h.Get("X-Custom") != "keep-me" {
		t.Errorf("X-Custom must survive, got %q", h.Get("X-Custom"))
	}
}
