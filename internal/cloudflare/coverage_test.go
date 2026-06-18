package cloudflare

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// quietLogger returns a non-nil logger whose Info/Debug output is suppressed by
// level; it exists only to exercise the logger-present branches in tunnel.go.
func quietLogger() *logger.Logger { return logger.New("error", "text") }

// shortBodyServer hijacks the connection and writes a Content-Length header that
// promises more bytes than it delivers, then closes — forcing io.ReadAll to
// return an unexpected-EOF error inside the client/iplist read paths.
func shortBodyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		// Promise 1000 bytes, send 3, then close abruptly.
		_, _ = bufrw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\nabc")
		_ = bufrw.Flush()
		_ = conn.(net.Conn).Close()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- Helper subprocess pattern ---------------------------------------------
//
// TestHelperProcess is re-invoked as a subprocess to stand in for the real
// cloudflared / apt / curl binaries. It is not a real test; it only does work
// when GO_HELPER_PROCESS=1 is set in the environment. Behaviour is driven by
// env vars so tests stay deterministic and never touch the network or the real
// cloudflared binary.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER_PROCESS") != "1" {
		return
	}
	// Emit any requested output first (used for version/install parsing).
	if out := os.Getenv("GO_HELPER_OUTPUT"); out != "" {
		fmt.Fprint(os.Stdout, out)
	}
	// Optionally block for a while to emulate a long-running process.
	if ms := os.Getenv("GO_HELPER_SLEEP_MS"); ms != "" {
		var d time.Duration
		fmt.Sscanf(ms, "%d", &d)
		time.Sleep(d * time.Millisecond)
	}
	if os.Getenv("GO_HELPER_EXIT") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

// helperCmd builds an *exec.Cmd that re-invokes the test binary as
// TestHelperProcess with the supplied environment knobs applied.
func helperCmd(env ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

// --- client.go: do() error paths -------------------------------------------

func TestDo_MarshalBodyError(t *testing.T) {
	c := New("tok", "acc")
	// A channel cannot be JSON-marshalled, forcing the marshal error branch.
	_, err := c.do("POST", "/x", make(chan int))
	if err == nil || !strings.Contains(err.Error(), "marshal body") {
		t.Fatalf("expected marshal body error, got %v", err)
	}
}

func TestDo_NewRequestError(t *testing.T) {
	c := New("tok", "acc")
	c.baseURL = "http://[::1]:namedport" // invalid URL → NewRequest fails
	_, err := c.do("GET", "/x", nil)
	if err == nil {
		t.Fatal("expected NewRequest error")
	}
}

func TestDo_TransportError(t *testing.T) {
	c := New("tok", "acc")
	// Point at a closed server so http.Client.Do fails.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c.baseURL = srv.URL
	srv.Close()
	_, err := c.do("GET", "/x", nil)
	if err == nil {
		t.Fatal("expected transport error after server closed")
	}
}

func TestDo_UnparseableJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.do("GET", "/x", nil)
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("expected parse response error, got %v", err)
	}
}

func TestDo_FailureWithoutErrorsArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[]}`))
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.do("GET", "/x", nil)
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("expected generic failure error, got %v", err)
	}
}

func TestDo_AuthorizationHeaderSet(t *testing.T) {
	var gotAuth, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		writeEnvelope(w, true, struct{}{}, nil)
	}))
	defer srv.Close()
	c := New("secret-token", "acc")
	c.baseURL = srv.URL
	if _, err := c.do("POST", "/x", map[string]any{"a": 1}); err != nil {
		t.Fatalf("do: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}
}

// --- client.go: JSON-parse error paths on each typed method ----------------

// badResultServer returns a server that wraps the supplied raw result JSON in a
// success envelope. Used to feed malformed result payloads to the typed parsers.
func badResultServer(t *testing.T, rawResult string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":` + rawResult + `}`))
	}))
	t.Cleanup(srv.Close)
	c := New("tok", "acc")
	c.baseURL = srv.URL
	return c
}

func TestCreateTunnel_ParseError(t *testing.T) {
	c := badResultServer(t, `"a string, not an object"`)
	if _, err := c.CreateTunnel("x"); err == nil || !strings.Contains(err.Error(), "parse tunnel") {
		t.Fatalf("expected parse tunnel error, got %v", err)
	}
}

func TestCreateTunnel_DoError(t *testing.T) {
	c := New("tok", "acc")
	c.baseURL = "http://[::1]:namedport"
	if _, err := c.CreateTunnel("x"); err == nil {
		t.Fatal("expected do error to propagate")
	}
}

func TestGetTunnelToken_ParseError(t *testing.T) {
	c := badResultServer(t, `{"not":"a string"}`)
	if _, err := c.GetTunnelToken("tid"); err == nil || !strings.Contains(err.Error(), "parse token") {
		t.Fatalf("expected parse token error, got %v", err)
	}
}

func TestGetTunnelToken_DoError(t *testing.T) {
	c := New("tok", "acc")
	c.baseURL = "http://[::1]:namedport"
	if _, err := c.GetTunnelToken("tid"); err == nil {
		t.Fatal("expected do error")
	}
}

func TestFindZoneByHostname_DoError(t *testing.T) {
	c := New("tok", "acc")
	c.baseURL = "http://[::1]:namedport"
	if _, err := c.FindZoneByHostname("a.example.com"); err == nil {
		t.Fatal("expected do error")
	}
}

func TestFindZoneByHostname_ParseError(t *testing.T) {
	c := badResultServer(t, `"not an array"`)
	if _, err := c.FindZoneByHostname("a.example.com"); err == nil || !strings.Contains(err.Error(), "parse zones") {
		t.Fatalf("expected parse zones error, got %v", err)
	}
}

func TestFindZoneByHostname_TrimsTrailingDot(t *testing.T) {
	c, _ := newTestClient(t)
	z, err := c.FindZoneByHostname("EXAMPLE.COM.")
	if err != nil {
		t.Fatalf("FindZoneByHostname: %v", err)
	}
	if z.ID != "zone-1" {
		t.Fatalf("expected zone-1, got %s", z.ID)
	}
}

func TestCreateTunnelCNAME_DoError(t *testing.T) {
	c := New("tok", "acc")
	c.baseURL = "http://[::1]:namedport"
	if _, err := c.CreateTunnelCNAME("z", "h", "tid"); err == nil {
		t.Fatal("expected do error")
	}
}

func TestCreateTunnelCNAME_ParseError(t *testing.T) {
	c := badResultServer(t, `"not an object"`)
	if _, err := c.CreateTunnelCNAME("z", "h", "tid"); err == nil || !strings.Contains(err.Error(), "parse record") {
		t.Fatalf("expected parse record error, got %v", err)
	}
}

func TestDeleteDNSRecord(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		writeEnvelope(w, true, struct{}{}, nil)
	}))
	defer srv.Close()
	c := New("tok", "acc")
	c.baseURL = srv.URL
	if err := c.DeleteDNSRecord("zone-9", "rec-9"); err != nil {
		t.Fatalf("DeleteDNSRecord: %v", err)
	}
	if hitPath != "/zones/zone-9/dns_records/rec-9" {
		t.Fatalf("unexpected path %q", hitPath)
	}
}

func TestDeleteDNSRecord_Error(t *testing.T) {
	c := New("tok", "acc")
	c.baseURL = "http://[::1]:namedport"
	if err := c.DeleteDNSRecord("z", "r"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNew_Defaults(t *testing.T) {
	c := New("tok", "acc")
	if c.token != "tok" || c.accountID != "acc" {
		t.Fatalf("unexpected client fields")
	}
	if c.baseURL != apiBase {
		t.Fatalf("baseURL = %q, want %q", c.baseURL, apiBase)
	}
	if c.http == nil || c.http.Timeout != 20*time.Second {
		t.Fatalf("http client not configured")
	}
}

// --- secret.go -------------------------------------------------------------

func TestRandomTunnelSecret(t *testing.T) {
	a := randomTunnelSecret()
	b := randomTunnelSecret()
	if a == "" || a == b {
		t.Fatalf("expected unique non-empty secrets, got %q / %q", a, b)
	}
	// 32 bytes std base64 → 44 chars.
	if len(a) != 44 {
		t.Fatalf("unexpected secret length %d", len(a))
	}
}

// --- ringbuffer.go: newRingBuffer default cap ------------------------------

func TestNewRingBuffer_DefaultCap(t *testing.T) {
	rb := newRingBuffer(0)
	if rb.cap != 32 {
		t.Fatalf("expected default cap 32, got %d", rb.cap)
	}
	rb2 := newRingBuffer(-5)
	if rb2.cap != 32 {
		t.Fatalf("expected default cap 32 for negative, got %d", rb2.cap)
	}
}

// --- iplist.go: error / cache paths ----------------------------------------

func TestContains_InvalidIP(t *testing.T) {
	set := NewIPSet()
	if set.Contains("not-an-ip", []string{"203.0.113.0/24"}) {
		t.Fatal("invalid IP should not match")
	}
}

func TestIPSet_CacheHitReusesNets(t *testing.T) {
	set := NewIPSet()
	cidrs := []string{"203.0.113.0/24"}
	// First call populates the cache.
	if !set.Contains("203.0.113.1", cidrs) {
		t.Fatal("expected match")
	}
	// Second call with same fingerprint should hit the cached fast path.
	if !set.Contains("203.0.113.2", cidrs) {
		t.Fatal("expected match on cached path")
	}
}

func TestNormalizeCIDRs_Invalid(t *testing.T) {
	if _, err := NormalizeCIDRs([]string{"999.999.999.999"}); err == nil {
		t.Fatal("expected invalid IP error")
	}
	if _, err := NormalizeCIDRs([]string{"10.0.0.0/99"}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
}

func TestNormalizeCIDRs_SkipsCommentsAndBlanks(t *testing.T) {
	got, err := NormalizeCIDRs([]string{"#comment", "  ", "203.0.113.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "203.0.113.0/24" {
		t.Fatalf("unexpected result %#v", got)
	}
}

func TestFetchIPRanges(t *testing.T) {
	// Stand up local servers and redirect the hardcoded CF URLs to them via a
	// custom RoundTripper installed on http.DefaultClient for the test.
	v4 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.0/24\n198.51.100.0/24\n"))
	}))
	defer v4.Close()
	v6 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("2001:db8::/32\n"))
	}))
	defer v6.Close()

	restore := redirectDefaultClient(map[string]string{
		IPv4ListURL: v4.URL,
		IPv6ListURL: v6.URL,
	})
	defer restore()

	got, err := FetchIPRanges(context.Background())
	if err != nil {
		t.Fatalf("FetchIPRanges: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 ranges, got %#v", got)
	}
}

func TestFetchIPRanges_V4Error(t *testing.T) {
	restore := redirectDefaultClient(map[string]string{
		IPv4ListURL: "http://127.0.0.1:0", // unroutable → error
	})
	defer restore()
	if _, err := FetchIPRanges(context.Background()); err == nil {
		t.Fatal("expected v4 fetch error")
	}
}

func TestFetchIPRanges_V6Error(t *testing.T) {
	v4 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.0/24\n"))
	}))
	defer v4.Close()
	restore := redirectDefaultClient(map[string]string{
		IPv4ListURL: v4.URL,
		IPv6ListURL: "http://127.0.0.1:0",
	})
	defer restore()
	if _, err := FetchIPRanges(context.Background()); err == nil {
		t.Fatal("expected v6 fetch error")
	}
}

func TestFetchIPRangeURL_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := fetchIPRangeURL(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}
}

func TestFetchIPRangeURL_BadURL(t *testing.T) {
	_, err := fetchIPRangeURL(context.Background(), "http://%zz")
	if err == nil {
		t.Fatal("expected request build error")
	}
}

func TestFetchIPRangeURL_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	_, err := fetchIPRangeURL(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

// redirectDefaultClient swaps http.DefaultClient.Transport for one that rewrites
// requests whose full URL matches a key to the mapped host. Returns a restore fn.
func redirectDefaultClient(remap map[string]string) func() {
	orig := http.DefaultClient.Transport
	http.DefaultClient.Transport = &remapTransport{remap: remap, base: http.DefaultTransport}
	return func() { http.DefaultClient.Transport = orig }
}

type remapTransport struct {
	remap map[string]string
	base  http.RoundTripper
}

func (rt *remapTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if target, ok := rt.remap[req.URL.String()]; ok {
		newReq := req.Clone(req.Context())
		u := newReq.URL
		// Parse target host:port into the request URL.
		parsed, err := http.NewRequest(req.Method, target, nil)
		if err != nil {
			return nil, err
		}
		u.Scheme = parsed.URL.Scheme
		u.Host = parsed.URL.Host
		u.Path = parsed.URL.Path
		return rt.base.RoundTrip(newReq)
	}
	return rt.base.RoundTrip(req)
}

// --- cloudflared.go: DetectCloudflared & InstallCloudflared ----------------

func TestDetectCloudflared_VersionViaHelper(t *testing.T) {
	if _, err := exec.LookPath("cloudflared"); err != nil {
		// DetectCloudflared resolves cloudflared via the real PATH; if it is not
		// installed the version-parse branch is unreachable without modifying
		// prod. Skip — this branch is exercised only when the binary exists.
		t.Skip("cloudflared not on PATH; DetectCloudflared version branch needs the binary present")
	}
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return helperCmd("GO_HELPER_OUTPUT=cloudflared version 9.9.9 (built today)")
	}
	info := DetectCloudflared()
	if !info.Installed || info.Version != "9.9.9" {
		t.Fatalf("unexpected info %+v", info)
	}
}

func TestDetectCloudflared_VersionCmdFails(t *testing.T) {
	if _, err := exec.LookPath("cloudflared"); err != nil {
		t.Skip("cloudflared not on PATH")
	}
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return helperCmd("GO_HELPER_EXIT=1")
	}
	info := DetectCloudflared()
	if !info.Installed {
		t.Fatal("expected installed even if version cmd fails")
	}
	if info.Version != "" {
		t.Fatalf("expected empty version on failure, got %q", info.Version)
	}
}

func TestInstallCloudflared_LinuxRunsSteps(t *testing.T) {
	// On non-linux the function refuses before reaching the exec steps, so the
	// step-execution branch is only reachable on linux. Run the step loop here;
	// the final DetectCloudflared() result depends on the real PATH, so we only
	// assert that the function returns without panicking and surfaces a sensible
	// outcome.
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	var calls int32
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		atomic.AddInt32(&calls, 1)
		// First few are install steps; DetectCloudflared also calls execCommandFn
		// for --version if cloudflared is on PATH. Make every call succeed.
		return helperCmd("GO_HELPER_OUTPUT=cloudflared version 1.2.3 (x)")
	}
	info, err := InstallCloudflared()
	_ = info
	_ = err
	if atomic.LoadInt32(&calls) == 0 {
		t.Skip("install steps not executed on this platform (non-linux)")
	}
	if calls < 4 {
		t.Fatalf("expected at least 4 install steps to run, got %d", calls)
	}
}

func TestInstallCloudflared_LinuxStepFailureRecorded(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	var calls int32
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		atomic.AddInt32(&calls, 1)
		// Fail the install steps so lastErr is set; DetectCloudflared at the end
		// determines whether the binary is "installed".
		return helperCmd("GO_HELPER_EXIT=1")
	}
	info, err := InstallCloudflared()
	if atomic.LoadInt32(&calls) == 0 {
		t.Skip("non-linux: install refused before steps")
	}
	// If cloudflared is genuinely on PATH, install may report success; otherwise
	// we expect an error surfaced from the failed steps or the not-on-PATH guard.
	if !info.Installed && err == nil {
		t.Fatal("expected an error when not installed")
	}
}

// --- tunnel.go: Runner lifecycle, monitor restart, Tail, Forget ------------

// fakeLongRunning makes execCommandFn spawn a helper that blocks for the given
// duration, emulating a healthy cloudflared process.
func fakeLongRunning(t *testing.T, sleepMS int) func() {
	t.Helper()
	orig := execCommandFn
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return helperCmd(fmt.Sprintf("GO_HELPER_SLEEP_MS=%d", sleepMS), "GO_HELPER_OUTPUT=hello-log\n")
	}
	return func() { execCommandFn = orig }
}

// waitFor polls cond until true or the deadline. The deadline is generous
// because the monitor's restart path has a hardcoded 2s backoff and the helper
// "process" is the test binary re-exec'd (slow under -race).
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func TestRunner_TailAndStatusEmptyForUnknown(t *testing.T) {
	r := NewRunner(nil)
	if r.Tail("nope") != "" {
		t.Fatal("expected empty tail for unknown tunnel")
	}
	st := r.StatusOf("nope")
	if st.Running {
		t.Fatal("expected not running")
	}
	if r.IsRunning("nope") {
		t.Fatal("expected not running")
	}
}

func TestRunner_TailReturnsLogs(t *testing.T) {
	restore := fakeLongRunning(t, 2000)
	defer restore()
	r := &Runner{procs: make(map[string]*runningProc), binary: "/fake/cloudflared"}
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop("t1") })

	waitFor(t, func() bool { return r.IsRunning("t1") }, "tunnel running")
	// drainOutput should capture the helper's stdout line.
	waitFor(t, func() bool { return strings.Contains(r.Tail("t1"), "hello-log") }, "log captured")
}

func TestRunner_SpawnLookPathFailure(t *testing.T) {
	// binary empty + cloudflared not on PATH → LookPath error path in spawn.
	if _, err := exec.LookPath("cloudflared"); err == nil {
		t.Skip("cloudflared is on PATH; cannot exercise LookPath failure")
	}
	r := NewRunner(nil) // binary == "" forces LookPath
	err := r.Start("t1", "tok")
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("expected LookPath failure, got %v", err)
	}
}

func TestRunner_StartCmdStartError(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	// Point cmd at a nonexistent program so cmd.Start() fails.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/definitely/not/here/binary-xyz")
	}
	r := &Runner{procs: make(map[string]*runningProc), binary: "/fake/cloudflared"}
	err := r.Start("t1", "tok")
	if err == nil || !strings.Contains(err.Error(), "start cloudflared") {
		t.Fatalf("expected start error, got %v", err)
	}
}

func TestRunner_MonitorAutoRestart(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	var spawnCount int32
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		atomic.AddInt32(&spawnCount, 1)
		// Exit almost immediately to trigger the unexpected-exit → restart path.
		return helperCmd("GO_HELPER_SLEEP_MS=10")
	}
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop("t1") })

	// The monitor uses a 2s backoff before restart; wait for at least one
	// restart (spawnCount >= 2).
	waitFor(t, func() bool { return atomic.LoadInt32(&spawnCount) >= 2 }, "auto-restart")
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestRunner_StopDuringBackoff(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	var spawnCount int32
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		atomic.AddInt32(&spawnCount, 1)
		return helperCmd("GO_HELPER_SLEEP_MS=10") // dies fast → enters 2s backoff
	}
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for the process to die and the monitor to enter its backoff window.
	waitFor(t, func() bool { return !r.IsRunning("t1") }, "process exited into backoff")
	// Stop during the backoff should break out without a further restart.
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	countAfterStop := atomic.LoadInt32(&spawnCount)
	// Wait past the 2s backoff window; a restart would have bumped spawnCount.
	// Because we stopped during backoff, no further spawn must occur.
	time.Sleep(2500 * time.Millisecond)
	if got := atomic.LoadInt32(&spawnCount); got != countAfterStop {
		t.Fatalf("expected no restart after stop-during-backoff, spawnCount %d -> %d", countAfterStop, got)
	}
	if r.IsRunning("t1") {
		t.Fatal("expected stopped")
	}
}

func TestRunner_GracefulStopNoRestart(t *testing.T) {
	restore := fakeLongRunning(t, 5000)
	defer restore()
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool { return r.IsRunning("t1") }, "running")
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Graceful stop path: monitor sees stopCh closed, sets cmd=nil, no restart.
	waitFor(t, func() bool { return !r.IsRunning("t1") }, "stopped via graceful path")
	// Confirm a fresh Start works again (stopCh was reset).
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop("t1") })
	waitFor(t, func() bool { return r.IsRunning("t1") }, "running again after restart")
}

func TestRunner_Forget(t *testing.T) {
	restore := fakeLongRunning(t, 5000)
	defer restore()
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool { return r.IsRunning("t1") }, "running")
	_ = r.Stop("t1")
	waitFor(t, func() bool { return !r.IsRunning("t1") }, "stopped")
	r.Forget("t1")
	if r.Tail("t1") != "" {
		t.Fatal("expected tail empty after Forget")
	}
	st := r.StatusOf("t1")
	if st.Running {
		t.Fatal("expected not running after Forget")
	}
}

func TestRunner_StatusOfRunning(t *testing.T) {
	restore := fakeLongRunning(t, 5000)
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
		t.Fatalf("expected uptime to be set")
	}
}

func TestRunner_StopKillErrorHandledGracefully(t *testing.T) {
	// Stop on a registered tunnel whose process already exited: cmd may be nil
	// (monitor cleared it) → Stop returns nil without attempting a kill.
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return helperCmd("GO_HELPER_SLEEP_MS=5")
	}
	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	// Register a proc manually with no cmd to hit the cmd==nil branch in Stop.
	r.mu.Lock()
	r.procs["manual"] = &runningProc{tunnelID: "manual", stopCh: make(chan struct{}), logTail: newRingBuffer(8)}
	r.mu.Unlock()
	if err := r.Stop("manual"); err != nil {
		t.Fatalf("Stop with nil cmd should be nil, got %v", err)
	}
}

func TestDo_ReadBodyError(t *testing.T) {
	srv := shortBodyServer(t)
	c := New("tok", "acc")
	c.baseURL = srv.URL
	_, err := c.do("GET", "/x", nil)
	if err == nil || !strings.Contains(err.Error(), "read response") {
		t.Fatalf("expected read response error, got %v", err)
	}
}

func TestFetchIPRangeURL_ReadBodyError(t *testing.T) {
	srv := shortBodyServer(t)
	_, err := fetchIPRangeURL(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected read body error")
	}
}

func TestRunner_MonitorNilCmd(t *testing.T) {
	// Directly exercise the cmd==nil early-return guard in monitor.
	r := NewRunner(quietLogger())
	p := &runningProc{tunnelID: "x", stopCh: make(chan struct{}), logTail: newRingBuffer(8)}
	r.monitor(p, "tok", nil, p.stopCh) // returns immediately, no goroutine spawned
}

func TestRunner_LoggerBranchesOnStartAndStop(t *testing.T) {
	restore := fakeLongRunning(t, 5000)
	defer restore()
	r := NewRunner(quietLogger()) // non-nil logger covers Info on spawn
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop("t1") })
	waitFor(t, func() bool { return r.IsRunning("t1") }, "running with logger")
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitFor(t, func() bool { return !r.IsRunning("t1") }, "stopped")
}

func TestRunner_MonitorWarnAndRestartWithLogger(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	var spawnCount int32
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		atomic.AddInt32(&spawnCount, 1)
		return helperCmd("GO_HELPER_SLEEP_MS=10") // exits fast → Warn + restart with logger
	}
	r := NewRunner(quietLogger())
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop("t1") })
	waitFor(t, func() bool { return atomic.LoadInt32(&spawnCount) >= 2 }, "restart-with-logger")
	_ = r.Stop("t1")
}

func TestRunner_MonitorRestartFailedWithLogger(t *testing.T) {
	// After the first process exits, make the next spawn fail (cmd.Start error)
	// so the monitor hits the "restart failed" logger branch.
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	var n int32
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if atomic.AddInt32(&n, 1) == 1 {
			return helperCmd("GO_HELPER_SLEEP_MS=10") // first run dies quickly
		}
		return exec.Command("/nonexistent/restart/target-xyz") // restart fails
	}
	r := NewRunner(quietLogger())
	r.binary = "/fake/cloudflared"
	if err := r.Start("t1", "tok"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop("t1") })
	// Wait for the failed restart attempt (n reaches 2).
	waitFor(t, func() bool { return atomic.LoadInt32(&n) >= 2 }, "failed restart attempt")
	// Give the monitor a moment to finish logging then stop.
	time.Sleep(50 * time.Millisecond)
	_ = r.Stop("t1")
}

func TestRunner_StopKillAlreadyExited(t *testing.T) {
	// Construct a runningProc whose cmd is an already-finished (reaped) process,
	// then call Stop. cmd.Process.Kill() on a finished process returns an error,
	// exercising Stop's kill-error branch deterministically and without involving
	// the monitor goroutine.
	cmd := helperCmd("GO_HELPER_SLEEP_MS=0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	_ = cmd.Wait() // process is now finished and reaped

	r := NewRunner(nil)
	r.binary = "/fake/cloudflared"
	r.mu.Lock()
	r.procs["t1"] = &runningProc{
		tunnelID: "t1",
		cmd:      cmd,
		stopCh:   make(chan struct{}),
		logTail:  newRingBuffer(8),
	}
	r.mu.Unlock()

	err := r.Stop("t1")
	if err == nil || !strings.Contains(err.Error(), "kill cloudflared") {
		t.Fatalf("expected kill error on finished process, got %v", err)
	}
	r.Forget("t1")
}

// --- helpers.go: errString / drainOutput edge -------------------------------

func TestErrString(t *testing.T) {
	if errString(nil) != "" {
		t.Fatal("nil error should be empty string")
	}
	if errString(fmt.Errorf("boom")) != "boom" {
		t.Fatal("expected boom")
	}
}
