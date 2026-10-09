package server

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// A TLS 1.2 handshake under a public domain's SNI must not reach a domain
// whose ssl.min_version is 1.3 by naming it in Host (F555).
func TestMinVersionDomainRefusesDowngradedSNI(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "public.example.com", Root: ccrRoot(t, "PUBLIC"), Type: "static", SSL: config.SSLConfig{Mode: "manual", MinVersion: "1.2"}},
			{Host: "strict.example.com", Root: ccrRoot(t, "STRICT"), Type: "static", SSL: config.SSLConfig{Mode: "manual", MinVersion: "1.3"}},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	_, _, srv := ccrCert(t, nil, nil, false, 5551, x509.ExtKeyUsageServerAuth, "public.example.com", "strict.example.com")
	s.tlsMgr.RegisterCert("public.example.com", &srv)
	s.tlsMgr.RegisterCert("strict.example.com", &srv)
	ts := httptest.NewUnstartedServer(s.handler)
	ts.TLS = s.tlsMgr.TLSConfig()
	ts.StartTLS()
	defer ts.Close()

	get := func(sni, host string, maxV uint16) (int, string) {
		t.Helper()
		tr := &http.Transport{TLSClientConfig: &tls.Config{ServerName: sni, InsecureSkipVerify: true, MaxVersion: maxV}}
		defer tr.CloseIdleConnections()
		req, _ := http.NewRequest("GET", ts.URL+"/index.html", nil)
		req.Host = host
		resp, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			t.Fatalf("sni=%s host=%s: %v", sni, host, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := get("public.example.com", "strict.example.com", tls.VersionTLS12); code != http.StatusMisdirectedRequest || body == "STRICT" {
		t.Errorf("TLS 1.2 via public SNI: got %d %q, want 421", code, body)
	}
	if code, body := get("public.example.com", "strict.example.com", 0); code != 200 || body != "STRICT" {
		t.Errorf("TLS 1.3 via public SNI: got %d %q, want 200 STRICT", code, body)
	}
	if code, body := get("public.example.com", "public.example.com", tls.VersionTLS12); code != 200 || body != "PUBLIC" {
		t.Errorf("public domain at TLS 1.2: got %d %q, want 200 PUBLIC", code, body)
	}
}
