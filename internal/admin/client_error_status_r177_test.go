package admin

// F42300–F42302: a request the client itself got wrong answered 500, which
// clients and monitors read as a server fault.
//   - SSH key add with a key that has an OpenSSH-looking prefix but does not
//     parse (the handler only checks the prefix), and key delete without a key
//     identifier, answered 500. The key was also parsed only AFTER the .ssh
//     directory had been created, so a rejected key still left a directory.
//   - DNS record create/update forwarded an empty type/name/content to the
//     provider, whose rejection came back as 500.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/dnsmanager"
)

func sshKeyServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "site", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := testServer()
	s.config.Domains = append(s.config.Domains, config.Domain{Host: "keys.example", Type: "static", Root: root})
	return s, filepath.Dir(root)
}

func TestSSHKeyAddMalformedKeyIs400AndLeavesNoDirectory(t *testing.T) {
	s, domainDir := sshKeyServer(t)
	cases := []struct {
		name, key string
	}{
		{"not base64", `ssh-rsa not-base64!!`},
		{"truncated blob", `ssh-rsa AAAAB3NzaC1 test`},
		{"unknown algorithm blob", `ssh-ed25519 AAAA`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/users/keys.example/ssh-keys",
				strings.NewReader(`{"public_key":"`+c.key+`"}`)))
			t.Logf("EXPECTED: 400\nACTUAL: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PROBLEM CONFIRMED: malformed key answered %d, want 400", rec.Code)
			}
			if _, err := os.Stat(filepath.Join(domainDir, ".ssh")); err == nil {
				t.Fatalf("PROBLEM CONFIRMED: rejected key still created %s", filepath.Join(domainDir, ".ssh"))
			}
		})
	}
}

// Control: a valid key is still accepted.
func TestSSHKeyAddValidKeyStillAccepted(t *testing.T) {
	s, _ := sshKeyServer(t)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/users/keys.example/ssh-keys",
		strings.NewReader(`{"public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid key = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestSSHKeyDeleteWithoutIdentifierIs400(t *testing.T) {
	s, _ := sshKeyServer(t)
	for _, body := range []string{`{}`, `{"fingerprint":""}`, `{"fingerprint":"   "}`} {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/users/keys.example/ssh-keys", strings.NewReader(body)))
		t.Logf("EXPECTED: 400\nACTUAL: %d %s (body %s)", rec.Code, strings.TrimSpace(rec.Body.String()), body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PROBLEM CONFIRMED: delete without identifier (%s) answered %d, want 400", body, rec.Code)
		}
	}
}

type countingDNSProvider struct {
	*stubProvider
	calls atomic.Int32
}

func (p *countingDNSProvider) CreateRecord(zoneID string, rec dnsmanager.Record) (*dnsmanager.Record, error) {
	p.calls.Add(1)
	return p.stubProvider.CreateRecord(zoneID, rec)
}

func (p *countingDNSProvider) UpdateRecord(zoneID, id string, rec dnsmanager.Record) (*dnsmanager.Record, error) {
	p.calls.Add(1)
	return p.stubProvider.UpdateRecord(zoneID, id, rec)
}

func TestDNSRecordWithoutRequiredFieldsIs400AndNeverReachesProvider(t *testing.T) {
	p := &countingDNSProvider{stubProvider: newStubProvider()}
	p.addZone("z1", "example.com")
	orig := testDNSProviderHook
	testDNSProviderHook = func() dnsmanager.Provider { return p }
	defer func() { testDNSProviderHook = orig }()
	s := testServer()

	bad := []string{
		`{}`,
		`{"type":"A","name":"www","content":""}`,
		`{"type":"","name":"www","content":"1.2.3.4"}`,
		`{"type":"A","name":"  ","content":"1.2.3.4"}`,
		`{"type":"A","name":"www","content":"1.2.3.4","ttl":-5}`,
		`{"type":"MX","name":"@","content":"mail.example.com","priority":-1}`,
		`{"type":"A B","name":"www","content":"1.2.3.4"}`,
	}
	for _, b := range bad {
		for _, route := range []struct{ method, path string }{
			{"POST", "/api/v1/dns/example.com/records"},
			{"PUT", "/api/v1/dns/example.com/records/rec-1"},
		} {
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, strings.NewReader(b)))
			t.Logf("EXPECTED: 400\nACTUAL: %d (%s %s)", rec.Code, route.method, b)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("PROBLEM CONFIRMED: %s %s answered %d, want 400", route.method, b, rec.Code)
			}
		}
	}
	if n := p.calls.Load(); n != 0 {
		t.Errorf("PROBLEM CONFIRMED: provider was called %d times for rejected records", n)
	}
}

// Control: complete records (including TTL 0 and 1 = automatic, and an MX
// with priority 0) still go through.
func TestDNSRecordCompleteRecordsStillAccepted(t *testing.T) {
	p := &countingDNSProvider{stubProvider: newStubProvider()}
	p.addZone("z1", "example.com")
	orig := testDNSProviderHook
	testDNSProviderHook = func() dnsmanager.Provider { return p }
	defer func() { testDNSProviderHook = orig }()
	s := testServer()
	for _, b := range []string{
		`{"type":"A","name":"www","content":"1.2.3.4","ttl":1}`,
		`{"type":"A","name":"www","content":"1.2.3.4"}`,
		`{"type":"MX","name":"@","content":"mail.example.com","ttl":300,"priority":0}`,
		`{"type":"TXT","name":"_dmarc","content":"v=DMARC1; p=none","ttl":300}`,
	} {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/records", strings.NewReader(b)))
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Errorf("complete record %s = %d %s, want 2xx", b, rec.Code, rec.Body.String())
		}
	}
}

// F42303: an invalid hostname is the caller's mistake for SFTP user create and
// delete too (it used to be 500 "user creation failed").
func TestSFTPUserInvalidHostnameIs400(t *testing.T) {
	s := testServer()
	for _, host := range []string{"bad host", "bad;host", "-lead.example", "a..b"} {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/users", strings.NewReader(`{"domain":"`+host+`"}`)))
		t.Logf("EXPECTED: 400\nACTUAL: %d %s (create %q)", rec.Code, strings.TrimSpace(rec.Body.String()), host)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("PROBLEM CONFIRMED: create for %q answered %d, want 400", host, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/users/bad%20host", nil))
	t.Logf("EXPECTED: 400\nACTUAL: %d %s (delete)", rec.Code, strings.TrimSpace(rec.Body.String()))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PROBLEM CONFIRMED: delete for an invalid hostname answered %d, want 400", rec.Code)
	}
}

func tarGzOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// F42304: an uploaded file that is not a cPanel backup is the caller's
// mistake (400), not a server fault (it used to be 500 "import failed").
func TestCPanelImportOfAnUnusableArchiveIs400(t *testing.T) {
	s, _ := testServerWithRoot(t)
	cases := []struct {
		name string
		data []byte
	}{
		{"not gzip", []byte("this is not a gzip stream")},
		{"gzip but not tar", func() []byte {
			var b bytes.Buffer
			gz := gzip.NewWriter(&b)
			gz.Write([]byte("plain text, not a tar archive"))
			gz.Close()
			return b.Bytes()
		}()},
		{"tar without any directory (no cPanel root)", tarGzOf(t, map[string]string{"readme.txt": "hello"})},
	}
	for _, c := range cases {
		rec := cpanelUploadpost(t, s, "backup", c.data)
		t.Logf("EXPECTED: 400\nACTUAL: %d %s (%s)", rec.Code, strings.TrimSpace(rec.Body.String()), c.name)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PROBLEM CONFIRMED: %s answered %d, want 400", c.name, rec.Code)
		}
	}
	// Control: a real cPanel archive still imports.
	if rec := cpanelUploadpost(t, s, "backup", cpanelUploadarchive(t)); rec.Code != http.StatusOK {
		t.Errorf("valid cPanel archive = %d %s, want 200", rec.Code, rec.Body.String())
	}
}
