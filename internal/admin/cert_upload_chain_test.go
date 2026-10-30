package admin

// F47304: the upload endpoint accepts the intermediate chain separately and
// wrote it to chain.pem, a file nothing ever reads: the manual loader reads
// only cert.pem, so a CA-issued leaf was served without its intermediates and
// clients failed chain validation. The chain is now part of cert.pem, the file
// the loader reads.
//
// F47305: an upload into a directory an ACME order had used kept that order's
// meta.json, so the uploaded pair still looked like an ACME-issued one to the
// store (see F47302) and kept the previous issuer in the metadata.

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

func uploadCertBody(t *testing.T, cert, key, chain string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"cert": cert, "key": key, "chain": chain})
	return string(b)
}

func TestCertUploadWithChainIsServedWithChain(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{{Host: "site.test", Type: "static"}})
	dir := t.TempDir()
	certUploadDir = dir
	t.Cleanup(func() { certUploadDir = "/var/lib/uwas/certs" })

	leaf, key := selfSignedPair(t, "site.test")
	intermediate, _ := selfSignedPair(t, "Test Intermediate CA")
	if code := e.do(http.MethodPost, "/api/v1/certs/site.test/upload", uploadCertBody(t, leaf, key, intermediate), auth.RoleAdmin); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "site.test", "cert.pem"), filepath.Join(dir, "site.test", "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: leaf + intermediate (2 certificates)\nACTUAL: %d", len(pair.Certificate))
	if len(pair.Certificate) != 2 {
		t.Fatalf("PROBLEM CONFIRMED: the served chain has %d certificate(s), want 2", len(pair.Certificate))
	}
}

func TestCertUploadControlsAndStaleMeta(t *testing.T) {
	setup := func(t *testing.T) (*hostConflictEnv, string) {
		e := newHostConflictEnv(t, []config.Domain{{Host: "site.test", Type: "static"}})
		dir := t.TempDir()
		certUploadDir = dir
		t.Cleanup(func() { certUploadDir = "/var/lib/uwas/certs" })
		return e, dir
	}

	t.Run("control: no chain keeps the single certificate", func(t *testing.T) {
		e, dir := setup(t)
		leaf, key := selfSignedPair(t, "site.test")
		if code := e.do(http.MethodPost, "/api/v1/certs/site.test/upload", uploadCertBody(t, leaf, key, ""), auth.RoleAdmin); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "site.test", "cert.pem"), filepath.Join(dir, "site.test", "key.pem"))
		if err != nil || len(pair.Certificate) != 1 {
			t.Fatalf("pair err=%v certs=%d, want 1", err, len(pair.Certificate))
		}
	})
	t.Run("chain that is not PEM certificates is rejected", func(t *testing.T) {
		e, dir := setup(t)
		leaf, key := selfSignedPair(t, "site.test")
		if code := e.do(http.MethodPost, "/api/v1/certs/site.test/upload", uploadCertBody(t, leaf, key, "not a pem chain"), auth.RoleAdmin); code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", code)
		}
		if _, err := os.Stat(filepath.Join(dir, "site.test")); err == nil {
			t.Fatal("a rejected upload left a certificate directory behind")
		}
	})
	t.Run("stale ACME meta.json is dropped by an upload", func(t *testing.T) {
		e, dir := setup(t)
		d := filepath.Join(dir, "site.test")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "meta.json"), []byte(`{"domain":"site.test","issuer":"R3"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		leaf, key := selfSignedPair(t, "site.test")
		if code := e.do(http.MethodPost, "/api/v1/certs/site.test/upload", uploadCertBody(t, leaf, key, ""), auth.RoleAdmin); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		_, err := os.Stat(filepath.Join(d, "meta.json"))
		t.Logf("EXPECTED: meta.json removed\nACTUAL: stat err = %v", err)
		if err == nil {
			t.Fatal("PROBLEM CONFIRMED: the upload kept the previous ACME order's meta.json")
		}
	})
}
