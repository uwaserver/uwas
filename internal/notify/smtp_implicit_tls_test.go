package notify

// F30001: port 465 (SMTPS) expects the TLS handshake before any SMTP traffic.
// sendMailBounded dialled plain TCP and waited for a banner the server never
// sends, so the alert e-mail failed after the full timeout.

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
	"strings"
	"testing"
	"time"
)

func selfSignedLoopback(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return cert, pool
}

// smtpServer accepts one message; implicitTLS wraps the listener in TLS.
func smtpServer(t *testing.T, cert *tls.Certificate) (port string, delivered <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if cert != nil {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{*cert}})
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(c)
		fmt.Fprint(c, "220 fake ESMTP\r\n")
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprint(c, "250 fake\r\n")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				fmt.Fprint(c, "250 ok\r\n")
			case strings.HasPrefix(line, "DATA"):
				fmt.Fprint(c, "354 go\r\n")
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				fmt.Fprint(c, "250 ok\r\n")
				got <- data.String()
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprint(c, "221 bye\r\n")
				return
			}
		}
	}()
	return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port), got
}

func TestSendMailImplicitTLSPort(t *testing.T) {
	origTimeout, origPort, origCfg := smtpTimeout, smtpImplicitTLSPort, smtpTLSConfig
	t.Cleanup(func() { smtpTimeout, smtpImplicitTLSPort, smtpTLSConfig = origTimeout, origPort, origCfg })
	smtpTimeout = 2 * time.Second // deadlock detector only
	cert, pool := selfSignedLoopback(t)
	smtpTLSConfig = func(host string) *tls.Config { return &tls.Config{ServerName: host, RootCAs: pool} }

	t.Run("implicit TLS server on the SMTPS port", func(t *testing.T) {
		port, delivered := smtpServer(t, &cert)
		smtpImplicitTLSPort = port
		err := sendMailBounded("127.0.0.1:"+port, nil, "a@example.com", []string{"b@example.com"}, []byte("Subject: x\r\n\r\nhello\r\n"))
		if err != nil {
			t.Fatalf("PROBLEM CONFIRMED: send to implicit-TLS server: %v", err)
		}
		if got := <-delivered; !strings.Contains(got, "hello") {
			t.Fatalf("message not delivered, got %q", got)
		}
	})
	t.Run("plain server on another port keeps the old path (control)", func(t *testing.T) {
		port, delivered := smtpServer(t, nil)
		smtpImplicitTLSPort = "465" // this port is not the SMTPS port
		if err := sendMailBounded("127.0.0.1:"+port, nil, "a@example.com", []string{"b@example.com"}, []byte("Subject: x\r\n\r\nplain\r\n")); err != nil {
			t.Fatalf("control: %v", err)
		}
		if got := <-delivered; !strings.Contains(got, "plain") {
			t.Fatalf("control message not delivered, got %q", got)
		}
	})
}
