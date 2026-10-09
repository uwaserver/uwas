package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func ccrCert(t *testing.T, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, ca bool, serial int64, eku x509.ExtKeyUsage, names ...string) (*x509.Certificate, *ecdsa.PrivateKey, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: names[0]},
		DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature}
	if ca {
		tmpl.IsCA, tmpl.BasicConstraintsValid = true, true
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{eku}
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, key, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func ccrRoot(t *testing.T, body string) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

// A client must not reach a client_auth=require domain by handshaking with
// another domain's SNI (or none) and naming the mTLS domain in Host.
func TestClientCertDomainRefusesMismatchedSNI(t *testing.T) {
	caCert, caKey, _ := ccrCert(t, nil, nil, true, 1, 0, "client ca")
	_, _, client := ccrCert(t, caCert, caKey, false, 2, x509.ExtKeyUsageClientAuth, "client")
	_, _, srv := ccrCert(t, nil, nil, false, 3, x509.ExtKeyUsageServerAuth, "public.example.com", "secure.example.com")
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "public.example.com", Root: ccrRoot(t, "PUBLIC"), Type: "static", SSL: config.SSLConfig{Mode: "manual"}},
			{Host: "secure.example.com", Root: ccrRoot(t, "SECRET"), Type: "static",
				SSL: config.SSLConfig{Mode: "manual", ClientCA: caPath, ClientAuth: "require"}},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	s.tlsMgr.RegisterCert("public.example.com", &srv)
	s.tlsMgr.RegisterCert("secure.example.com", &srv)
	if err := s.tlsMgr.LoadClientCAs(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.handler)
	ts.TLS = s.tlsMgr.TLSConfig()
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	port := ts.URL[strings.LastIndex(ts.URL, ":"):]

	get := func(sni, host string, cert *tls.Certificate, h2 bool) (int, string) {
		t.Helper()
		tc := &tls.Config{ServerName: sni, InsecureSkipVerify: true}
		if cert != nil {
			tc.Certificates = []tls.Certificate{*cert}
		}
		tr := &http.Transport{TLSClientConfig: tc, ForceAttemptHTTP2: h2}
		defer tr.CloseIdleConnections()
		req, _ := http.NewRequest("GET", "https://127.0.0.1"+port+"/index.html", nil)
		req.Host = host
		resp, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			t.Fatalf("sni=%q host=%q: %v", sni, host, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	cases := []struct {
		name, sni, host string
		cert            *tls.Certificate
		h2              bool
		code            int
	}{
		{"matching SNI with client cert", "secure.example.com", "secure.example.com", &client, false, 200},
		{"public SNI, secure Host", "public.example.com", "secure.example.com", nil, false, 421},
		{"public SNI, secure Host, cert offered", "public.example.com", "secure.example.com", &client, false, 421},
		{"no SNI, secure Host", "", "secure.example.com", nil, false, 421},
		{"coalesced HTTP/2", "public.example.com", "secure.example.com", nil, true, 421},
		{"public domain unaffected", "public.example.com", "public.example.com", nil, false, 200},
	}
	for _, tc := range cases {
		code, body := get(tc.sni, tc.host, tc.cert, tc.h2)
		if code != tc.code || (tc.code != 200 && body == "SECRET") {
			t.Errorf("%s: got %d %.20q, want %d", tc.name, code, body, tc.code)
		}
	}
}

// While its certificate is still pending, a client_auth=require domain must
// not fall through to plain HTTP, where no client certificate can be asked for.
func TestClientCertRequireDomainNotServedOverPlainHTTP(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("unused"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "public.example.com", Root: ccrRoot(t, "PUBLIC"), Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "secure.example.com", Root: ccrRoot(t, "SECRET"), Type: "static",
				SSL: config.SSLConfig{Mode: "auto", ClientCA: caPath, ClientAuth: "require"}},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	ts := httptest.NewServer(http.HandlerFunc(s.handleHTTP))
	defer ts.Close()

	for host, want := range map[string]int{"secure.example.com": 403, "public.example.com": 200} {
		req, _ := http.NewRequest("GET", ts.URL+"/index.html", nil)
		req.Host = host
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want || (want != 200 && string(b) == "SECRET") {
			t.Errorf("%s over plain HTTP: got %d, want %d", host, resp.StatusCode, want)
		}
	}
}
