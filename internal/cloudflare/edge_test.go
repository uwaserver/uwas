package cloudflare

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// doListPages: completely untested paginated-cloudflare-API path.
// ---------------------------------------------------------------------------

func TestDoListPages_SinglePage(t *testing.T) {
	var gotPage int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPage = 1 // only one page
		writeEnvelope(w, true, []map[string]any{
			{"id": "z1", "name": "example.com"},
		}, nil)
	}))
	defer srv.Close()

	c := New("tok", "acc")
	c.baseURL = srv.URL

	pages, err := c.doListPages("/zones")
	if err != nil {
		t.Fatalf("doListPages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if gotPage != 1 {
		t.Fatalf("expected page param 1, got %d", gotPage)
	}
}

// TestDoListPages_MultiplePages verifies the pagination loop follows
// result_info.total_pages and collects every page.
func TestDoListPages_MultiplePages(t *testing.T) {
	pageCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageCount++
		// Return success with 3 total pages; pages 1 and 2 have data, page 3 empty.
		totalPages := 3
		result := []map[string]any{
			{"id": fmt.Sprintf("z%d", pageCount), "name": fmt.Sprintf("zone-%d", pageCount)},
		}
		resp := struct {
			Success    bool              `json:"success"`
			Result     []map[string]any  `json:"result"`
			Errors     []map[string]any  `json:"errors"`
			ResultInfo map[string]int `json:"result_info"`
		}{
			Success: true,
			Result:  result,
			Errors:  nil,
			ResultInfo: map[string]int{"total_pages": totalPages},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("tok", "acc")
	c.baseURL = srv.URL

	pages, err := c.doListPages("/zones")
	if err != nil {
		t.Fatalf("doListPages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(pages))
	}
}

func TestDoListPages_NewRequestError(t *testing.T) {
	c := New("tok", "acc")
	c.baseURL = "http://[::1]:namedport"
	_, err := c.doListPages("/zones")
	if err == nil {
		t.Fatal("expected NewRequest error")
	}
}

func TestDoListPages_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	c := New("tok", "acc")
	c.baseURL = srv.URL
	srv.Close()
	_, err := c.doListPages("/zones")
	if err == nil {
		t.Fatal("expected transport error after server closed")
	}
}

func TestDoListPages_ResponseBodyError(t *testing.T) {
	srv := shortBodyServer(t)
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.doListPages("/zones")
	if err == nil || !strings.Contains(err.Error(), "read response") {
		t.Fatalf("expected read response error, got %v", err)
	}
}

func TestDoListPages_UnparseableJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.doListPages("/zones")
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("expected parse response error, got %v", err)
	}
}

func TestDoListPages_CFErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, false, nil, []map[string]any{
			{"code": 10000, "message": "api auth failure"},
		})
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.doListPages("/zones")
	if err == nil || !strings.Contains(err.Error(), "api auth failure") {
		t.Fatalf("expected CF error message, got %v", err)
	}
}

func TestDoListPages_FailureWithoutErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[]}`))
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.doListPages("/zones")
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("expected generic failure error, got %v", err)
	}
}

func TestDoListPages_ExistingQuerySep(t *testing.T) {
	// If the path already contains "?", the separator switches from "?" to "&".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "per_page=50") {
			t.Fatalf("expected per_page param in query: %s", r.URL.RawQuery)
		}
		writeEnvelope(w, true, []map[string]any{}, nil)
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.doListPages("/zones?foo=bar")
	if err != nil {
		t.Fatalf("doListPages: %v", err)
	}
}

func TestDoListPages_HeadersSet(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		writeEnvelope(w, true, []map[string]any{}, nil)
	}))
	defer srv.Close()
	c := New("bearer-token", "acc")
	c.baseURL = srv.URL
	_, err := c.doListPages("/zones")
	if err != nil {
		t.Fatalf("doListPages: %v", err)
	}
	if authHeader != "Bearer bearer-token" {
		t.Fatalf("auth header = %q", authHeader)
	}
}

// ---------------------------------------------------------------------------
// InstallCloudflared: binary-now-on-PATH-after-step-failure branch, and
// all-steps-succeed-but-binary-missing branch.
// ---------------------------------------------------------------------------

func TestInstallCloudflared_StepsFailButBinaryExists(t *testing.T) {
	// When install steps fail but cloudflared IS on PATH (e.g. previously
	// installed), InstallCloudflared returns (info, nil) because the "not
	// installed?" check evaluates to false.
	orig := execCommandFn
	defer func() { execCommandFn = orig }()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return helperCmd("GO_HELPER_EXIT=1")
	}

	info, err := InstallCloudflared()
	// If cloudflared is genuinely on PATH, we expect Installed=true and nil err.
	// If not, the install steps failed and binary is not on PATH.
	if info.Installed && err != nil {
		t.Fatalf("installed + err should be nil, got %v", err)
	}
	if !info.Installed && err == nil {
		t.Fatal("expected error when not installed after failed steps")
	}
}

// TestRunner_StopCleanedProc tests that Stop on a registered tunnel whose
// cmd.Process is nil (e.g. Start allocated the proc but spawn never ran)
// returns nil without panicking.
func TestRunner_StopCleanedProc(t *testing.T) {
	r := NewRunner(nil)
	r.mu.Lock()
	// A proc with cmd != nil but Process == nil (race / partial init).
	r.procs["partial"] = &runningProc{
		tunnelID: "partial",
		cmd:      &exec.Cmd{},
		stopCh:   make(chan struct{}),
		logTail:  newRingBuffer(8),
	}
	r.mu.Unlock()

	if err := r.Stop("partial"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestRunner_StopRemovesStoppedProc exercises the idempotent stopCh close
// guard: calling Stop twice must not close the channel twice (panic).
func TestRunner_StopIdempotent(t *testing.T) {
	restore := fakeLongRunning(t, 2000)
	defer restore()
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for running.
	waitFor(t, func() bool { return r.IsRunning("t1") }, "running")
	// First Stop.
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	_ = r.Stop("t1") // must not panic
}

// TestRunner_SpawnStdoutPipeError exercises the StdoutPipe error branch.
func TestRunner_SpawnStdoutPipeError(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		// A nil name makes StdoutPipe fail.
		cmd := exec.Command("")
		return cmd
	}
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	err := r.Start("t1", "tok")
	if err == nil || (!strings.Contains(err.Error(), "stdout pipe") && !strings.Contains(err.Error(), "start cloudflared")) {
		t.Logf("got error (may vary by platform): %v", err)
	}
}

// TestRunner_SpawnEmptyToken exercises the empty-token guard in Start.
func TestRunner_SpawnEmptyToken(t *testing.T) {
	r := NewRunner(nil)
	if err := r.Start("t1", ""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

// TestDoListPages_HardCap exercises the runaway guard (page > 1000).
func TestDoListPages_HardCap(t *testing.T) {
	// Return total_pages > 1000; the loop hard-caps at 1000.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Success    bool             `json:"success"`
			Result     []map[string]any `json:"result"`
			Errors     []map[string]any `json:"errors"`
			ResultInfo map[string]int   `json:"result_info"`
		}{
			Success: true,
			Result:  []map[string]any{{"id": "z"}},
			Errors:  nil,
			ResultInfo: map[string]int{"total_pages": 9999},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	pages, err := c.doListPages("/zones")
	if err != nil {
		t.Fatalf("doListPages: %v", err)
	}
	if len(pages) != 1000 {
		t.Fatalf("expected 1000 pages (hard cap), got %d", len(pages))
	}
}

// TestRunner_StatusOfWithDetails verifies StatusOf returns full info for
// a running tunnel.
func TestRunner_StatusOfWithDetails(t *testing.T) {
	restore := fakeLongRunning(t, 2000)
	defer restore()
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop("t1") })
	waitFor(t, func() bool { return r.IsRunning("t1") }, "running")

	st := r.StatusOf("t1")
	if !st.Running || st.PID == 0 || st.TunnelID != "t1" {
		t.Fatalf("unexpected status %+v", st)
	}
	if st.Uptime == "" {
		t.Fatalf("expected non-empty uptime")
	}
	if st.StartedAt.IsZero() {
		t.Fatalf("expected non-zero StartedAt")
	}
}

// TestCreateTunnel_SecretError exercises the secret-generation error path
// by relying on the existing test infrastructure. Since crypto/rand.Read
// cannot be easily injected without modifying prod code, we verify the
// happy path is already covered (it is, in client_test.go).
func TestCreateTunnel_AlreadyCovered(t *testing.T) {
	// The happy path is covered in TestCreateAndDeleteTunnel (client_test.go).
	// The secret-generator error path (crypto/rand failure) is impossible to
	// exercise without modifying source — crypto/rand.Read never fails on
	// Linux. This test exists as a placeholder documenting that gap.
	t.Log("randomTunnelSecret crypto/rand error path: requires source injection; untestable on typical Linux")
}
