package cli

// F32202 / F32203: the CLI built admin API URLs by concatenating the host or PHP
// version typed on the command line straight into the path, and `php start`
// pasted --port into a JSON string with %s.
//   - "domain remove 'a?b'" sent the DELETE to /api/v1/domains/a with a query
//     string, and "cert renew a/b" addressed a different route;
//   - "php start 8.4 --port abc" asked the server to start PHP-CGI on
//     127.0.0.1:abc, and a quote in --port produced invalid JSON.
// Path segments are escaped and --port must be a TCP port before any request.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func uriRecorder(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var uris []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uris = append(uris, r.Method+" "+r.RequestURI)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &uris
}

func TestAPIPathSegmentsAreEscaped(t *testing.T) {
	srv, uris := uriRecorder(t)
	_ = (&DomainCommand{}).remove([]string{"--api-url", srv.URL, "a?b/c"})
	_ = (&CertCommand{}).renew("a?b/c", []string{"--api-url", srv.URL})
	_ = (&PHPCommand{}).stop([]string{"--api-url", srv.URL, "8.4/../x"})
	got := strings.Join(*uris, "\n")
	t.Logf("EXPECTED: every segment percent-encoded\nACTUAL:\n%s", got)
	for _, want := range []string{
		"DELETE /api/v1/domains/a%3Fb%2Fc",
		"POST /api/v1/certs/a%3Fb%2Fc/renew",
		"POST /api/v1/php/8.4%2F..%2Fx/stop",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("PROBLEM CONFIRMED: missing %q", want)
		}
	}
}

func TestAPIPathPlainValuesUnchanged(t *testing.T) {
	srv, uris := uriRecorder(t)
	_ = (&DomainCommand{}).remove([]string{"--api-url", srv.URL, "example.com"})
	_ = (&PHPCommand{}).stop([]string{"--api-url", srv.URL, "8.4"})
	got := strings.Join(*uris, "\n")
	for _, want := range []string{"DELETE /api/v1/domains/example.com", "POST /api/v1/php/8.4/stop"} {
		if !strings.Contains(got, want) {
			t.Fatalf("control: missing %q in %s", want, got)
		}
	}
}

func TestPHPStartRejectsInvalidPort(t *testing.T) {
	for _, port := range []string{"abc", "0", "65536", "-1", `9"x`, "9000 ", ""} {
		srv, uris := uriRecorder(t)
		err := (&PHPCommand{}).start([]string{"8.4", "--api-url", srv.URL, "--port", port})
		t.Logf("port %q: err=%v requests=%d", port, err, len(*uris))
		if err == nil || len(*uris) != 0 {
			t.Fatalf("PROBLEM CONFIRMED: --port %q accepted (err=%v, requests=%v)", port, err, *uris)
		}
	}
}

func TestPHPStartAcceptsValidPort(t *testing.T) {
	for _, port := range []string{"1", "9000", "65535"} {
		srv, uris := uriRecorder(t)
		if err := (&PHPCommand{}).start([]string{"8.4", "--api-url", srv.URL, "--port", port}); err != nil {
			t.Fatalf("--port %s: %v", port, err)
		}
		if len(*uris) != 1 {
			t.Fatalf("--port %s: requests = %v", port, *uris)
		}
	}
}
