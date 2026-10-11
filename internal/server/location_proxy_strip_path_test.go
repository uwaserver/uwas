package server

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// F2620: a location proxy_pass with strip_prefix builds the upstream URL by
// string-concatenating loc.ProxyPass with the DECODED request path. A match
// without trailing slash ("/api") leaves a remainder that need not start with
// "/", so "/api@host:port/x" turns the configured upstream into userinfo and
// the attacker-chosen host into the authority; and a decoded "%3F" / "%23"
// is re-read as the start of the query / fragment.

type f2620Backend struct {
	mu   sync.Mutex
	hits []string
}

func (b *f2620Backend) record(r *http.Request) {
	b.mu.Lock()
	b.hits = append(b.hits, r.RequestURI)
	b.mu.Unlock()
}

func (b *f2620Backend) seen() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.hits...)
}

func f2620Listen(t *testing.T, ip string) (*f2620Backend, string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Skipf("cannot listen on %s: %v", ip, err)
	}
	b := &f2620Backend{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.record(r)
		fmt.Fprint(w, "UP")
	}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	return b, ln.Addr().String(), srv.Close
}

func f2620Server(t *testing.T, addrA string, strip bool) *Server {
	return nonCanonServer(t, "", fmt.Sprintf(`    proxy: {allow_private_upstreams: true}
    locations:
      - match: /api
        proxy_pass: http://%s
        strip_prefix: %v
`, addrA, strip))
}

func f2620Get(s *Server, target string) int {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "a.test"
	req.RemoteAddr = "192.0.2.5:1"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestLocationProxyStripPrefixKeepsConfiguredUpstream(t *testing.T) {
	a, addrA, closeA := f2620Listen(t, "127.0.0.1")
	defer closeA()
	b, addrB, closeB := f2620Listen(t, "127.0.0.2")
	defer closeB()
	s := f2620Server(t, addrA, true)

	cases := []struct{ req, want string }{
		{"/api/x", "/x"},                              // control
		{"/api/x?q=1", "/x?q=1"},                      // query kept
		{"/api", "/"},                                 // bare match
		{"/api/", "/"},                                // trailing slash
		{"/apixyz", "/xyz"},                           // remainder is rooted
		{"/api/a%3Fb", "/a%3Fb"},                      // encoded ? stays in the path
		{"/api/a%23b", "/a%23b"},                      // encoded # stays in the path
		{"/api/a%2Fb", "/a%2Fb"},                      // encoded slash preserved
		{"/api@" + addrB + "/x", "/@" + addrB + "/x"}, // never an authority
	}
	for _, c := range cases {
		before := len(a.seen())
		if code := f2620Get(s, c.req); code != http.StatusOK {
			t.Fatalf("%s = %d", c.req, code)
		}
		got := a.seen()
		if len(got) != before+1 || got[len(got)-1] != c.want {
			t.Errorf("%s: upstream A saw %v, want last %q", c.req, got[before:], c.want)
		}
	}
	if h := b.seen(); len(h) != 0 {
		t.Errorf("attacker-named host was contacted: %v", h)
	}
}

func TestLocationProxyWithoutStripPrefixUnchanged(t *testing.T) {
	a, addrA, closeA := f2620Listen(t, "127.0.0.1")
	defer closeA()
	s := f2620Server(t, addrA, false)
	for _, c := range []struct{ req, want string }{
		{"/api/x?q=1", "/api/x?q=1"},
		{"/api/a%3Fb", "/api/a%3Fb"},
	} {
		before := len(a.seen())
		if code := f2620Get(s, c.req); code != http.StatusOK {
			t.Fatalf("%s = %d", c.req, code)
		}
		got := a.seen()
		if len(got) != before+1 || got[len(got)-1] != c.want {
			t.Errorf("%s: upstream saw %v, want last %q", c.req, got[before:], c.want)
		}
	}
}
