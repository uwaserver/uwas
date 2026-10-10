package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// mirrorSink records the headers and request URI of every request it gets.
func mirrorSink(t *testing.T) (*httptest.Server, func() ([]http.Header, []string)) {
	t.Helper()
	var mu sync.Mutex
	var hdrs []http.Header
	var uris []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hdrs = append(hdrs, r.Header.Clone())
		uris = append(uris, r.URL.RequestURI())
		mu.Unlock()
	}))
	t.Cleanup(ts.Close)
	return ts, func() ([]http.Header, []string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]http.Header(nil), hdrs...), append([]string(nil), uris...)
	}
}

// The X-Mirror marker tells the shadow backend a request is a copy. A client
// must not be able to delete it by naming it in Connection (F900).
func TestMirrorMarkerSurvivesConnectionHeader(t *testing.T) {
	ts, got := mirrorSink(t)
	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())

	for _, conn := range []string{"X-Mirror", "keep-alive, x-mirror"} {
		req := httptest.NewRequest("POST", "/pay", strings.NewReader("b"))
		req.Header.Set("Connection", conn)
		m.doMirror(req, []byte("b")) // synchronous
		h, _ := got()
		if v := h[len(h)-1].Get("X-Mirror"); v != "true" {
			t.Errorf("Connection=%q: X-Mirror=%q, want true", conn, v)
		}
	}
}

// The mirror URL must keep the configured shadow host even when the request
// path has no leading slash, as after a relative rewrite target (F902).
func TestMirrorURLKeepsShadowHost(t *testing.T) {
	shadow, shadowGot := mirrorSink(t)
	other, otherGot := mirrorSink(t)
	m := NewMirror(MirrorConfig{Enabled: true, Backend: shadow.URL, Percent: 100}, testLogger())
	otherHost := strings.TrimPrefix(other.URL, "http://")

	for _, c := range []struct{ path, want string }{
		{"/index.php", "/index.php"},
		{"index.php", "/index.php"},
		{"@" + otherHost + "/steal", "/@" + otherHost + "/steal"},
	} {
		req := httptest.NewRequest("GET", "/x", nil)
		req.URL = &url.URL{Path: c.path}
		_, before := shadowGot()
		m.doMirror(req, nil)
		_, after := shadowGot()
		if len(after) != len(before)+1 || after[len(after)-1] != c.want {
			t.Errorf("path %q: shadow got %v, want a new %q", c.path, after, c.want)
		}
	}
	if h, _ := otherGot(); len(h) != 0 {
		t.Errorf("a request reached another host: %d", len(h))
	}
}
