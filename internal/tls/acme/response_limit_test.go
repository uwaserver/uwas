package acme

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// F53000: a CA response was read without a bound; an oversized chain was
// buffered whole and an oversized JSON document decoded without limit.
func TestDownloadCertRejectsOversizedChain(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := []byte(strings.Repeat("A", 1<<20))
		for i := 0; i < maxACMEBody/(1<<20)+2; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	if got, err := stub.client.downloadCert(context.Background(), stub.orderURL); err == nil {
		t.Fatalf("downloadCert accepted a %d byte chain", len(got))
	}
}

func TestGetAuthorizationBoundedJSON(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"` + strings.Repeat("a", maxACMEBody+10) + `"}`))
	})
	if _, err := stub.client.getAuthorization(context.Background(), stub.orderURL); err == nil {
		t.Fatal("getAuthorization decoded a document larger than the bound")
	}
}
