package cli

// F28500: flags written after the positional argument were silently dropped
// because flag.FlagSet.Parse stops at the first non-flag. The documented
// examples ("domain add example.com --type static --root /var/www --ssl auto",
// "php start 8.4 --port 9001") therefore created a domain with the defaults
// and started PHP on port 9000. Only "php uninstall" used parseInterspersed.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func recordingServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.Method+" "+r.URL.Path+" "+string(b))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestDomainAddAcceptsFlagsAfterHost(t *testing.T) {
	srv, bodies := recordingServer(t)
	args := []string{"example.com", "--api-url", srv.URL, "--type", "php", "--root", "/var/www", "--ssl", "auto"}
	if err := (&DomainCommand{}).add(args); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := strings.Join(*bodies, "\n")
	t.Logf("EXPECTED: type php, root /var/www, ssl auto\nACTUAL: %s", got)
	for _, want := range []string{`"type":"php"`, `"root":"/var/www"`, `"mode":"auto"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("PROBLEM CONFIRMED: %s missing from request: %s", want, got)
		}
	}
}

func TestDomainAddFlagsBeforeHostControl(t *testing.T) {
	srv, bodies := recordingServer(t)
	args := []string{"--api-url", srv.URL, "--type", "php", "example.com"}
	if err := (&DomainCommand{}).add(args); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(*bodies, ""), `"type":"php"`) {
		t.Fatalf("control failed: %v", *bodies)
	}
}

func TestDomainRemoveAcceptsFlagsAfterHost(t *testing.T) {
	srv, bodies := recordingServer(t)
	if err := (&DomainCommand{}).remove([]string{"old.example.com", "--api-url", srv.URL}); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: remove: %v", err)
	}
	if got := strings.Join(*bodies, ""); !strings.Contains(got, "DELETE /api/v1/domains/old.example.com") {
		t.Fatalf("unexpected request: %s", got)
	}
}

func TestPHPStartAcceptsPortAfterVersion(t *testing.T) {
	srv, bodies := recordingServer(t)
	if err := (&PHPCommand{}).start([]string{"8.4", "--port", "9001", "--api-url", srv.URL}); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: start: %v", err)
	}
	got := strings.Join(*bodies, "")
	t.Logf("EXPECTED: 127.0.0.1:9001\nACTUAL: %s", got)
	if !strings.Contains(got, "127.0.0.1:9001") {
		t.Fatalf("PROBLEM CONFIRMED: --port after version ignored: %s", got)
	}
}

func TestPHPStopConfigExtensionsAcceptTrailingFlags(t *testing.T) {
	srv, bodies := recordingServer(t)
	p := &PHPCommand{}
	// extensions expects a JSON array and config an object; only the request
	// path matters here, so ignore decode errors.
	_ = p.stop([]string{"8.4", "--api-url", srv.URL})
	_ = p.config([]string{"8.4", "--api-url", srv.URL})
	_ = p.extensions([]string{"8.4", "--api-url", srv.URL})
	got := strings.Join(*bodies, "\n")
	for _, want := range []string{"POST /api/v1/php/8.4/stop", "GET /api/v1/php/8.4/config", "GET /api/v1/php/8.4/extensions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("PROBLEM CONFIRMED: %q not requested via --api-url after version; got %q", want, got)
		}
	}
}
