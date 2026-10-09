package cloudflare

// Regression tests for tunnel state lifecycle: Disconnect/Connect must stop the
// tunnels they drop (F435), and overlapping state-mutating handlers must not
// lose each other's changes (F436).
//
// tsDeps mirrors the admin adapter (cfDeps): LoadCloudflareState returns a deep
// copy, SaveCloudflareState replaces the stored state wholesale.

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

type tsDeps struct {
	mu       sync.Mutex
	st       *State
	running  map[string]bool
	stops    []string
	forgets  []string
	createFn func(name, hostname string) (Tunnel, error)
}

func newTSDeps(st *State) *tsDeps {
	return &tsDeps{st: st, running: map[string]bool{}}
}

func tsCloneState(st *State) *State {
	if st == nil {
		return nil
	}
	c := *st
	c.Tunnels = append([]Tunnel(nil), st.Tunnels...)
	return &c
}

func (d *tsDeps) RequireAdmin(http.ResponseWriter, *http.Request) bool { return true }
func (d *tsDeps) ValidateToken(token, accountID string) (string, error) {
	return "e@x", nil
}
func (d *tsDeps) FetchZones(string) ([]Zone, error)                   { return nil, nil }
func (d *tsDeps) FetchDNSRecords(string, string) ([]DNSRecord, error) { return nil, nil }
func (d *tsDeps) PurgeCache(string, string, bool) error               { return nil }
func (d *tsDeps) NormalizeCIDRs(r []string) ([]string, error)         { return r, nil }
func (d *tsDeps) LogInfo(string, ...any)                              {}
func (d *tsDeps) LogWarn(string, ...any)                              {}
func (d *tsDeps) LogError(string, ...any)                             {}
func (d *tsDeps) RecordAudit(*http.Request, string, string, bool)     {}
func (d *tsDeps) CloudflareIPRanges() ([]string, string)              { return nil, "" }
func (d *tsDeps) SetCloudflareIPRanges([]string, string)              {}
func (d *tsDeps) PersistConfig() error                                { return nil }
func (d *tsDeps) NotifyDomainChange()                                 {}
func (d *tsDeps) LoadCloudflareState() *State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return tsCloneState(d.st)
}
func (d *tsDeps) SaveCloudflareState(st *State) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if st == nil || (!st.Connected && st.Token == "") {
		d.st = nil
	} else {
		d.st = tsCloneState(st)
	}
	return nil
}
func (d *tsDeps) CreateTunnelAPI(token, accountID, name, hostname, localTarget string) (Tunnel, error) {
	if d.createFn != nil {
		return d.createFn(name, hostname)
	}
	return Tunnel{ID: "id-" + name, Name: name, Hostname: hostname, LocalTarget: localTarget, ConnectorToken: "tok"}, nil
}
func (d *tsDeps) DeleteTunnelAPI(string, string, string) error { return nil }
func (d *tsDeps) TunnelStatusOf(id string) (bool, int, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running[id], 0, ""
}
func (d *tsDeps) TunnelStart(id, token string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running[id] = true
	return nil
}
func (d *tsDeps) TunnelStop(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stops = append(d.stops, id)
	if !d.running[id] {
		return fmt.Errorf("tunnel %s not running", id)
	}
	delete(d.running, id)
	return nil
}
func (d *tsDeps) TunnelForget(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.forgets = append(d.forgets, id)
}
func (d *tsDeps) TunnelTail(string) string                      { return "" }
func (d *tsDeps) AddDomain(config.Domain)                       {}
func (d *tsDeps) ExistingDomains() map[string]bool              { return map[string]bool{} }
func (d *tsDeps) WebRoot() string                               { return "" }
func (d *tsDeps) FetchIPRanges(*http.Request) ([]string, error) { return nil, nil }
func (d *tsDeps) isRunning(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running[id]
}
func (d *tsDeps) tunnelNames() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.st == nil {
		return nil
	}
	var out []string
	for _, t := range d.st.Tunnels {
		out = append(out, t.Name)
	}
	return out
}

func tsReq(method, body, id string) *http.Request {
	r := httptest.NewRequest(method, "/x", bytes.NewBufferString(body))
	if id != "" {
		r.SetPathValue("id", id)
	}
	return r
}

func tsCreate(h *Handler, name string) int {
	w := httptest.NewRecorder()
	h.TunnelCreate(w, tsReq("POST", fmt.Sprintf(`{"name":%q,"hostname":"%s.example.com","local_target":"http://localhost:80"}`, name, name), ""))
	return w.Code
}

// tsWaitParked polls goroutine stacks until a goroutine inside fn is parked
// acquiring a sync.Mutex.
func tsWaitParked(fn string) bool {
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, fn) && strings.Contains(g, "sync.(*Mutex).Lock") {
				return true
			}
		}
		runtime.Gosched()
	}
	return false
}

func TestDisconnectStopsRunningTunnels(t *testing.T) {
	d := newTSDeps(&State{Token: "t", AccountID: "acc", Connected: true, Tunnels: []Tunnel{
		{ID: "t1", Name: "one", ConnectorToken: "c1"}, {ID: "t2", Name: "two", ConnectorToken: "c2"},
	}})
	h := New(d)
	d.TunnelStart("t1", "c1")
	h.Disconnect(httptest.NewRecorder(), tsReq("POST", "", ""))
	if d.isRunning("t1") {
		t.Fatal("tunnel t1 still running after disconnect")
	}
	sort.Strings(d.forgets)
	if got := strings.Join(d.forgets, ","); got != "t1,t2" {
		t.Fatalf("forgets = %q, want t1,t2", got)
	}
}

func TestReconnectStopsDroppedTunnels(t *testing.T) {
	d := newTSDeps(&State{Token: "t", AccountID: "acc", Connected: true, Tunnels: []Tunnel{{ID: "t3", Name: "three", ConnectorToken: "c3"}}})
	d.TunnelStart("t3", "c3")
	w := httptest.NewRecorder()
	New(d).Connect(w, tsReq("POST", `{"token":"new","account_id":"acc2"}`, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("connect = %d", w.Code)
	}
	if d.isRunning("t3") {
		t.Fatal("tunnel t3 still running after reconnect dropped it from state")
	}
}

func TestConcurrentTunnelCreateKeepsBoth(t *testing.T) {
	d := newTSDeps(&State{Token: "t", AccountID: "acc", Connected: true})
	entered := map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}
	release := map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}
	d.createFn = func(name, host string) (Tunnel, error) {
		close(entered[name])
		<-release[name]
		return Tunnel{ID: "id-" + name, Name: name, Hostname: host, ConnectorToken: "c"}, nil
	}
	h := New(d)
	da, db := make(chan int, 1), make(chan int, 1)
	go func() { da <- tsCreate(h, "a") }()
	<-entered["a"]
	go func() { db <- tsCreate(h, "b") }()
	// b must wait for a; if it reaches the CF API concurrently, finish b first
	// so a's stale snapshot is saved last (the lost-update order).
	select {
	case <-entered["b"]:
		close(release["b"])
		<-db
		close(release["a"])
		<-da
	case <-func() chan struct{} {
		c := make(chan struct{})
		go func() {
			if tsWaitParked("(*Handler).TunnelCreate") {
				close(c)
			}
		}()
		return c
	}():
		close(release["a"])
		<-da
		<-entered["b"]
		close(release["b"])
		<-db
	}
	names := d.tunnelNames()
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "a,b" {
		t.Fatalf("tunnels = %q, want a,b", got)
	}
}
