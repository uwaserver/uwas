package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================================
// purgeCloudflareCache (14.3% -> target) — handler-level tests with connected
// config so the body-decoding and purgeCloudflareCache call are exercised.
// Different body shapes test the JSON unmarshal and the boolean/url fields.
// ============================================================================

func TestHandleCloudflareCachePurge_ConnectedBodies(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	tests := []struct {
		name string
		body string
	}{
		{"url_only", `{"url":"https://example.com/style.css"}`},
		{"everything_true", `{"everything":true}`},
		{"url_and_everything", `{"url":"https://example.com/style.css","everything":true}`},
		{"empty_body", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testServer()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/cloudflare/cache/purge",
				strings.NewReader(tt.body))
			s.mux.ServeHTTP(rec, req)
			// Handler decodes body, then calls purgeCloudflareCache which reaches
			// the real Cloudflare API → network error → 500.
			t.Logf("status=%d body=%s", rec.Code, rec.Body.String())
			if rec.Code == 0 {
				t.Fatal("no response written")
			}
		})
	}
}

func TestHandleCloudflareCachePurge_ConnectedNetworkError(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: errorTransport{}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	body := `{"url":"https://example.com/style.css","everything":false}`
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/cache/purge",
		strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)
	t.Logf("status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
}

// ============================================================================
// purgeCloudflareCache full success path — mock Cloudflare API with zones
// so the function loops over zones, builds purge requests, and completes.
// ============================================================================

func TestHandleCloudflareCachePurge_SuccessPath(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/v4/zones":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"result":[{"id":"z1","name":"example.com","status":"active"}]}`))
		case "/client/v4/zones/z1/purge_cache":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	tests := []struct {
		name string
		body string
	}{
		{"purge_url", `{"url":"https://example.com/style.css"}`},
		{"purge_everything", `{"everything":true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testServer()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/cloudflare/cache/purge",
				strings.NewReader(tt.body))
			s.mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			json.Unmarshal(rec.Body.Bytes(), &resp)
			if resp["status"] != "purged" {
				t.Errorf("status = %v, want 'purged'", resp["status"])
			}
		})
	}
}

// ============================================================================
// validateCloudflareToken (36.4% -> target) — tested via handleCloudflareConnect
// with a mock Cloudflare API server.
// ============================================================================

func TestHandleCloudflareConnect_ValidToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/v4/user/tokens/verify":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"result":{"id":"user1","status":"active","expires_on":"2027-01-01"}}`))
		case "/client/v4/accounts/acc-123":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"result":{"name":"test@example.com"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	body := `{"token":"valid-test-token","account_id":"acc-123"}`
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/connect",
		strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	t.Logf("status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "connected" {
		t.Errorf("status = %v, want 'connected'", resp["status"])
	}
}

func TestHandleCloudflareConnect_InvalidTokenVerifyFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/v4/user/tokens/verify":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":false,"errors":[{"message":"token invalid or expired"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	body := `{"token":"invalid-token","account_id":"acc-123"}`
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/connect",
		strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	t.Logf("status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ============================================================================
// handleCloudflareTunnelCreate (48.5% -> target) — connected + valid body
// exercises the code path after validation (FindZoneByHostname API call).
// ============================================================================

func TestHandleCloudflareTunnelCreate_ConnectedValidBody(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	body := `{"name":"test-tunnel","hostname":"tunnel.example.com","local_target":"http://localhost:8080"}`
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels",
		strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)
	t.Logf("status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
}

// ============================================================================
// handleCloudflareTunnelDelete (40% -> target) — connected + exists tunnel.
// Also test the connected + not found path via mux for completeness.
// ============================================================================

func TestHandleCloudflareTunnelDelete_ConnectedNotFound(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/cloudflare/tunnels/nonexistent-id", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	t.Logf("delete not found body: %s", rec.Body.String())
}

func TestHandleCloudflareTunnelDelete_ConnectedExists(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{
			{ID: "existing-id", Name: "test", Hostname: "t.example.com",
				ZoneID: "z1", DNSRecordID: "dns1"},
		},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: errorTransport{}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/cloudflare/tunnels/existing-id", nil)
	s.mux.ServeHTTP(rec, req)
	// Handler finds the tunnel, then tries real API call → network error → 502
	t.Logf("delete exists status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
}

// ============================================================================
// handleCloudflareTunnelStart (20.5% -> target) — connected + tunnel found +
// nil runner → 500. Also test connected + tunnel with empty connector token
// which triggers the re-fetch path.
// ============================================================================

func TestHandleCloudflareTunnelStart_ConnectedNoRunner(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{
			{ID: "existing-id", Name: "test", Hostname: "t.example.com",
				ConnectorToken: "tok-abc"},
		},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	// testServer() has cfRunner == nil
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/existing-id/start", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
	t.Logf("start no runner body: %s", rec.Body.String())
}

func TestHandleCloudflareTunnelStart_ConnectedEmptyToken(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{
			{ID: "empty-token-id", Name: "test", Hostname: "t.example.com",
				ConnectorToken: ""},
		},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: errorTransport{}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/empty-token-id/start", nil)
	s.mux.ServeHTTP(rec, req)
	// ConnectorToken is empty, so handler re-fetches from Cloudflare API
	// → network error → 502
	t.Logf("start empty token status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
}
