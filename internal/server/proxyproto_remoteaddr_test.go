package server

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestProxyProtocolRemoteAddrReachesHandler pins F366: net/http records
// RemoteAddr() before its first Read, so the PROXY header must be parsed by
// then or every request carries the load balancer's address. Both the plain
// and the tcp -> proxyproto -> tls order used by startHTTPS are covered.
func TestProxyProtocolRemoteAddrReachesHandler(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pp.test"},
		DNSNames:     []string{"pp.test"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(4102444800, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}

	for _, useTLS := range []bool{false, true} {
		tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var ln net.Listener = newProxyProtoListener(tcpLn)
		if useTLS {
			ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
		}
		got := make(chan string, 1)
		srv := &http.Server{
			Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got <- r.RemoteAddr }),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() { _ = srv.Serve(ln) }()

		raw, err := net.DialTimeout("tcp", tcpLn.Addr().String(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := raw.Write([]byte("PROXY TCP4 203.0.113.7 10.0.0.1 56324 443\r\n")); err != nil {
			t.Fatal(err)
		}
		var c net.Conn = raw
		if useTLS {
			tc := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: "pp.test"})
			if err := tc.Handshake(); err != nil {
				t.Fatal(err)
			}
			c = tc
		}
		fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: pp.test\r\nConnection: close\r\n\r\n")
		if resp, err := http.ReadResponse(bufio.NewReader(c), nil); err == nil {
			resp.Body.Close()
		}
		select {
		case ra := <-got:
			if ra != "203.0.113.7:56324" {
				t.Errorf("tls=%v: r.RemoteAddr = %q, want the PROXY source 203.0.113.7:56324", useTLS, ra)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("tls=%v: handler never ran", useTLS)
		}
		raw.Close()
		srv.Close()
	}
}
