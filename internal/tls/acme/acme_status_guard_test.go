package acme

// Regression: finalizeOrder, getAuthorization and downloadCert must treat a
// non-2xx ACME problem document (RFC 7807) as an error that names the cause.
//
// Bug (fixed by adding the same non-2xx guard waitForStatus already had to the
// three remaining response handlers): the body was decoded without a status
// check, so a problem document yielded a zero-value Order/Authorization (or
// raw problem JSON as certPEM) with a NIL error. Observable worst case:
// a finalize rejection (400 badCSR, 429 rateLimited, ...) decoded into
// Order{Status:""} and ObtainCertificate polled the dead order for "valid"
// for its whole 30-attempt budget (waits 1+2+...+30 ≈ 8 minutes) before
// reporting "timeout waiting for status" instead of the server's diagnostic.
// getAuthorization misreported the same class as "no supported challenge
// available"; downloadCert surfaced it as a keypair parse error.
//
// ensureDirectory, ensureAccount, newOrder and waitForStatus already checked
// statuses; this pins the last three call sites to the same contract.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"net/http"
	"strings"
	"testing"
)

// respondJSON writes a 200 response with the given JSON body.
func respondJSON(w http.ResponseWriter, body string) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

func TestFinalizeOrderSurfacesACMEProblemDocument(t *testing.T) {
	tests := []struct {
		name    string
		code    int
		typ     string
		detail  string
		wantSub string
	}{
		{
			name:    "bad csr",
			code:    http.StatusBadRequest,
			typ:     "urn:ietf:params:acme:error:badCSR",
			detail:  "finalization rejected by test",
			wantSub: "badCSR",
		},
		{
			name:    "rate limited",
			code:    http.StatusTooManyRequests,
			typ:     "urn:ietf:params:acme:error:rateLimited",
			detail:  "too many certificates",
			wantSub: "429",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
				problemDoc(w, tt.code, tt.typ, tt.detail)
			})
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			csrDER, err := x509.CreateCertificateRequest(rand.Reader,
				&x509.CertificateRequest{DNSNames: []string{"example.com"}}, key)
			if err != nil {
				t.Fatal(err)
			}

			order, err := stub.client.finalizeOrder(context.Background(), stub.orderURL, csrDER)
			if err == nil {
				t.Fatalf("finalizeOrder accepted HTTP %d problem document as an order (status %q)", tt.code, order.Status)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestFinalizeOrderSuccessControl(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, `{"status":"ready","finalize":"https://acme.example.com/finalize"}`)
	})
	order, err := stub.client.finalizeOrder(context.Background(), stub.orderURL, []byte("csr"))
	if err != nil {
		t.Fatalf("finalizeOrder on a 200 order failed: %v", err)
	}
	if order.Status != "ready" {
		t.Errorf("status = %q, want %q", order.Status, "ready")
	}
}

func TestGetAuthorizationSurfacesACMEProblemDocument(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		problemDoc(w, http.StatusForbidden,
			"urn:ietf:params:acme:error:unauthorized",
			"account is not authorized")
	})

	authz, err := stub.client.getAuthorization(context.Background(), stub.orderURL)
	if err == nil {
		t.Fatalf("getAuthorization accepted HTTP 403 problem document as an authorization (status %q, challenges %d)", authz.Status, len(authz.Challenges))
	}
	if !strings.Contains(err.Error(), "unauthorized") || !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, want it to name the problem type and status", err.Error())
	}
}

func TestGetAuthorizationSuccessControl(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, `{"status":"valid","identifier":{"type":"dns","value":"example.com"},"challenges":[]}`)
	})
	authz, err := stub.client.getAuthorization(context.Background(), stub.orderURL)
	if err != nil {
		t.Fatalf("getAuthorization on a 200 authorization failed: %v", err)
	}
	if authz.Status != "valid" {
		t.Errorf("status = %q, want %q", authz.Status, "valid")
	}
}

func TestDownloadCertSurfacesACMEProblemDocument(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		problemDoc(w, http.StatusServiceUnavailable,
			"urn:ietf:params:acme:error:serverInternal",
			"certificate is not available yet")
	})

	certPEM, err := stub.client.downloadCert(context.Background(), stub.orderURL)
	if err == nil {
		t.Fatalf("downloadCert accepted HTTP 503 problem document as a certificate chain (%d bytes of problem JSON as certPEM)", len(certPEM))
	}
	if !strings.Contains(err.Error(), "serverInternal") {
		t.Errorf("error = %q, want it to name the problem type", err.Error())
	}
}

func TestDownloadCertSuccessControl(t *testing.T) {
	chain := "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n"
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(chain))
	})
	got, err := stub.client.downloadCert(context.Background(), stub.orderURL)
	if err != nil {
		t.Fatalf("downloadCert on a 200 chain failed: %v", err)
	}
	if string(got) != chain {
		t.Errorf("downloaded %q, want the stub chain", got)
	}
}
