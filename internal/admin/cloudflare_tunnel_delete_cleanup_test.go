package admin

// Regression for TunnelDelete leaving Cloudflare resources behind: the tunnel's
// DNS CNAME was never deleted (hostname kept pointing at a dead tunnel and
// could not be reused), and the Cloudflare delete was sent while the connector
// was still running, which Cloudflare rejects, orphaning the tunnel. The fake
// API answers "active connections" while the fake cloudflared is alive; it
// learns that from FIFO EOF when the process dies, not from timing.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	cfintegration "github.com/uwaserver/uwas/internal/cloudflare"
	"github.com/uwaserver/uwas/internal/logger"
)

type cfDelRecord struct{ Zone, ID, Name, Content string }

type cfDelFakeAPI struct {
	mu          sync.Mutex
	tunnels     map[string]bool
	records     map[string]cfDelRecord // key: zone+"/"+id
	seq         int
	failToken   bool
	connected   map[string]func() bool // tunnelID -> "does CF still see a connection?"
	tunnelDels  []string
	recordDels  []string
	dnsDelFails bool
}

func newCFDelFakeAPI() *cfDelFakeAPI {
	return &cfDelFakeAPI{tunnels: map[string]bool{}, records: map[string]cfDelRecord{}, connected: map[string]func() bool{}}
}

func (f *cfDelFakeAPI) addTunnel(id, host, zone, recID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tunnels[id] = true
	f.records[zone+"/"+recID] = cfDelRecord{Zone: zone, ID: recID, Name: host, Content: id + ".cfargotunnel.com"}
}

func (f *cfDelFakeAPI) hasTunnel(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tunnels[id]
}

func (f *cfDelFakeAPI) recordsFor(host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.records {
		if r.Name == host {
			n++
		}
	}
	return n
}

func cfDelWrite(w http.ResponseWriter, code int, ok bool, result any, errCode int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body := map[string]any{"success": ok, "result": result, "errors": []any{}}
	if !ok {
		body["errors"] = []any{map[string]any{"code": errCode, "message": msg}}
	}
	body["result_info"] = map[string]any{"total_pages": 1}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *cfDelFakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/client/v4")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case r.Method == "GET" && p == "/zones":
		cfDelWrite(w, 200, true, []map[string]string{{"id": "z1", "name": "example.com"}}, 0, "")
	case r.Method == "POST" && len(parts) == 3 && parts[0] == "accounts" && parts[2] == "cfd_tunnel":
		var body struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.seq++
		id := fmt.Sprintf("tun-new-%d", f.seq)
		f.tunnels[id] = true
		f.mu.Unlock()
		cfDelWrite(w, 200, true, map[string]string{"id": id, "name": body.Name}, 0, "")
	case r.Method == "PUT" && len(parts) == 5 && parts[4] == "configurations":
		cfDelWrite(w, 200, true, map[string]any{}, 0, "")
	case r.Method == "GET" && len(parts) == 5 && parts[4] == "token":
		if f.failToken {
			cfDelWrite(w, 500, false, nil, 1000, "token fetch failed")
			return
		}
		cfDelWrite(w, 200, true, "conn-token", 0, "")
	case r.Method == "DELETE" && len(parts) == 4 && parts[2] == "cfd_tunnel":
		id := parts[3]
		f.mu.Lock()
		f.tunnelDels = append(f.tunnelDels, id)
		conn := f.connected[id]
		f.mu.Unlock()
		// Documented contract (internal/cloudflare/client.go DeleteTunnel):
		// "Will fail if the tunnel still has active connections."
		if conn != nil && conn() {
			cfDelWrite(w, 400, false, nil, 1022, "Cannot delete tunnel because it has active connections")
			return
		}
		f.mu.Lock()
		delete(f.tunnels, id)
		f.mu.Unlock()
		cfDelWrite(w, 200, true, map[string]string{"id": id}, 0, "")
	case r.Method == "POST" && len(parts) == 3 && parts[0] == "zones" && parts[2] == "dns_records":
		var body struct{ Name, Content string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		for _, rec := range f.records {
			if rec.Name == body.Name {
				f.mu.Unlock()
				cfDelWrite(w, 400, false, nil, 81053, "An A, AAAA, or CNAME record with that host already exists.")
				return
			}
		}
		f.seq++
		id := fmt.Sprintf("rec-new-%d", f.seq)
		f.records[parts[1]+"/"+id] = cfDelRecord{Zone: parts[1], ID: id, Name: body.Name, Content: body.Content}
		f.mu.Unlock()
		cfDelWrite(w, 200, true, map[string]string{"id": id}, 0, "")
	case r.Method == "DELETE" && len(parts) == 4 && parts[0] == "zones" && parts[2] == "dns_records":
		f.mu.Lock()
		f.recordDels = append(f.recordDels, parts[1]+"/"+parts[3])
		if f.dnsDelFails {
			f.mu.Unlock()
			cfDelWrite(w, 500, false, nil, 1000, "dns delete failed")
			return
		}
		delete(f.records, parts[1]+"/"+parts[3])
		f.mu.Unlock()
		cfDelWrite(w, 200, true, map[string]string{"id": parts[3]}, 0, "")
	default:
		cfDelWrite(w, 404, false, nil, 7003, "no route "+r.Method+" "+p)
	}
}

type cfDelTransport struct{ h http.Handler }

func (rt cfDelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.cloudflare.com" {
		return nil, errors.New("cfDel: unexpected host " + req.URL.Host)
	}
	rec := httptest.NewRecorder()
	rt.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// cfDelInstall routes the cloudflare client (http.Client with nil Transport →
// http.DefaultTransport) and the admin cfHTTPClient to the fake. No network.
func cfDelInstall(t *testing.T, f *cfDelFakeAPI) {
	t.Helper()
	origDT := http.DefaultTransport
	origCF := cfHTTPClient
	http.DefaultTransport = cfDelTransport{h: f}
	cfHTTPClient = &http.Client{Transport: cfDelTransport{h: f}}
	t.Cleanup(func() {
		http.DefaultTransport = origDT
		cfHTTPClient = origCF
	})
}

func cfDelSetState(tunnels ...cloudflareTunnel) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "tok", AccountID: "acc", Connected: true, Tunnels: tunnels}
	cloudflareMu.Unlock()
}

func cfDelClearState() {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()
}

func cfDelStateHas(id string) bool {
	cloudflareMu.RLock()
	defer cloudflareMu.RUnlock()
	if cloudflareConfig == nil {
		return false
	}
	for _, t := range cloudflareConfig.Tunnels {
		if t.ID == id {
			return true
		}
	}
	return false
}

// cfDelConnector starts a fake cloudflared (shell script on PATH) through the
// real cfintegration.Runner. The fake holds the write end of a FIFO; the read
// end reports EOF exactly when the process has died. connected() therefore
// answers "is the connector still up?" from a real kernel event; the deadline
// is only a hang guard that classifies a connector that stays up.
type cfDelConnector struct {
	rd *os.File
}

func cfDelStartConnector(t *testing.T, s *Server, tunnelID string) *cfDelConnector {
	t.Helper()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "conn.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	rd, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open fifo rd: %v", err)
	}
	dummy, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open fifo wr: %v", err)
	}
	script := "#!/bin/sh\nexec 3>\"" + fifo + "\"\nprintf R >&3\nexec /bin/sleep 1000000\n"
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0755)
	if err := os.WriteFile(filepath.Join(bin, "cloudflared"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if s.cfRunner == nil {
		s.cfRunner = cfintegration.NewRunner(logger.New("error", "text"))
	}
	if err := s.cfRunner.Start(tunnelID, "conn-token"); err != nil {
		t.Fatalf("runner start: %v", err)
	}
	t.Cleanup(func() { _ = s.cfRunner.Stop(tunnelID); _ = rd.Close() })
	buf := make([]byte, 1)
	_ = rd.SetReadDeadline(time.Now().Add(10 * time.Second))
	if n, err := rd.Read(buf); n != 1 || buf[0] != 'R' {
		t.Fatalf("fake connector never became ready: n=%d err=%v", n, err)
	}
	_ = dummy.Close()
	return &cfDelConnector{rd: rd}
}

func (c *cfDelConnector) connected() bool {
	_ = c.rd.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1)
	_, err := c.rd.Read(buf)
	return !errors.Is(err, io.EOF)
}

func TestCloudflareTunnelDeleteRemovesCNAME(t *testing.T) {
	defer cfDelClearState()
	f := newCFDelFakeAPI()
	cfDelInstall(t, f)
	f.addTunnel("tun-idle", "idle.example.com", "z1", "rec-idle")
	cfDelSetState(cloudflareTunnel{ID: "tun-idle", Name: "idle", Hostname: "idle.example.com",
		LocalTarget: "http://localhost:8080", ConnectorToken: "ct", ZoneID: "z1", DNSRecordID: "rec-idle"})
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/cloudflare/tunnels/tun-idle", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if n := f.recordsFor("idle.example.com"); n != 0 {
		t.Fatalf("CNAME records left after delete = %d, want 0", n)
	}
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels",
		strings.NewReader(`{"name":"idle2","hostname":"idle.example.com","local_target":"http://localhost:8080"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("recreating a tunnel for the same hostname: status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestCloudflareTunnelDeleteStopsConnectorFirst(t *testing.T) {
	defer cfDelClearState()
	f := newCFDelFakeAPI()
	cfDelInstall(t, f)
	f.addTunnel("tun-run", "run.example.com", "z1", "rec-run")
	cfDelSetState(cloudflareTunnel{ID: "tun-run", Name: "run", Hostname: "run.example.com",
		LocalTarget: "http://localhost:8080", ConnectorToken: "ct", ZoneID: "z1", DNSRecordID: "rec-run"})
	s := testServer()
	c := cfDelStartConnector(t, s, "tun-run")
	f.mu.Lock()
	f.connected["tun-run"] = c.connected
	f.mu.Unlock()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/cloudflare/tunnels/tun-run", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if cfDelStateHas("tun-run") {
		t.Fatal("tunnel still in UWAS state")
	}
	if f.hasTunnel("tun-run") {
		t.Fatal("tunnel still exists at Cloudflare: delete was sent while the connector was running")
	}
	if f.recordsFor("run.example.com") != 0 {
		t.Fatal("CNAME left behind")
	}
}
