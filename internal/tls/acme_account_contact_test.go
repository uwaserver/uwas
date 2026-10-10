package uwastls

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// acmeAccountPayload runs ObtainCertificate against a fake CA that only
// implements directory/nonce/newAccount and returns the decoded newAccount
// payload (the CA rejects the order, which ends the flow).
func acmeAccountPayload(t *testing.T, email string) map[string]any {
	t.Helper()
	var mu sync.Mutex
	var payload map[string]any
	nonce := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		nonce++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("n-%d", nonce))
		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(map[string]string{
				"newNonce": srv.URL + "/new-nonce", "newAccount": srv.URL + "/new-acct", "newOrder": srv.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			var jws struct {
				Payload string `json:"payload"`
			}
			json.NewDecoder(r.Body).Decode(&jws)
			raw, _ := base64.RawURLEncoding.DecodeString(jws.Payload)
			payload = map[string]any{}
			json.Unmarshal(raw, &payload)
			w.Header().Set("Location", srv.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := config.ACMEConfig{Email: email, CAURL: srv.URL + "/directory", Storage: t.TempDir()}
	m := NewManager(cfg, nil, logger.New("error", "text"))
	if m.acme == nil {
		t.Fatal("acme client not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, _, _ = m.acme.ObtainCertificate(ctx, []string{"example.com"})
	mu.Lock()
	defer mu.Unlock()
	return payload
}

// TestACMEAccountRegisteredWithContactEmail: acme.email was only a gate for
// enabling ACME; the newAccount request carried no contact, so the CA could
// never send expiry or revocation notices to the operator (F1660).
func TestACMEAccountRegisteredWithContactEmail(t *testing.T) {
	cases := []struct {
		name  string
		email string
		want  []any
	}{
		{"plain", "ops@example.com", []any{"mailto:ops@example.com"}},
		{"trimmed", "  ops@example.com \n", []any{"mailto:ops@example.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := acmeAccountPayload(t, tc.email)
			if p == nil || p["termsOfServiceAgreed"] != true {
				t.Fatalf("no newAccount request / ToS not agreed: %v", p)
			}
			got, _ := p["contact"].([]any)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("contact = %v, want %v", got, tc.want)
			}
		})
	}
}

// A blank contact must not be sent as an empty mailto: URL, which CAs reject.
func TestACMEAccountOmitsEmptyContact(t *testing.T) {
	var payload map[string]any
	nonce := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("n-%d", nonce))
		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(map[string]string{"newNonce": srv.URL + "/new-nonce", "newAccount": srv.URL + "/new-acct", "newOrder": srv.URL + "/new-order"})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			var jws struct {
				Payload string `json:"payload"`
			}
			json.NewDecoder(r.Body).Decode(&jws)
			raw, _ := base64.RawURLEncoding.DecodeString(jws.Payload)
			payload = map[string]any{}
			json.Unmarshal(raw, &payload)
			w.Header().Set("Location", srv.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	m2 := NewManager(config.ACMEConfig{Email: "x@example.com", CAURL: srv.URL + "/directory", Storage: t.TempDir()}, nil, logger.New("error", "text"))
	m2.acme.SetContactEmail("   ")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, _, _ = m2.acme.ObtainCertificate(ctx, []string{"example.com"})
	if payload == nil {
		t.Fatal("no newAccount request")
	}
	if _, ok := payload["contact"]; ok {
		t.Fatalf("blank email sent a contact: %v", payload["contact"])
	}
}
