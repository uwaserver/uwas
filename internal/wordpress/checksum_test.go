package wordpress

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// checksumTestServer fronts a tarball + a configurable subset of
// checksum suffixes. Set publish to e.g. {"sha1": true, "sha256": true}
// to make the corresponding .sha1 / .sha256 URLs return the hash; the
// rest return 404. URL is the base URL of the tarball; suffix
// derivation is `URL + "." + algo`.
type checksumTestServer struct {
	tarBody []byte
	publish map[string]bool
	srv     *httptest.Server
}

func newChecksumTestServer(tarBody string, publish map[string]bool) *checksumTestServer {
	c := &checksumTestServer{
		tarBody: []byte(tarBody),
		publish: publish,
	}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Determine which suffix (if any) was requested.
		for _, algo := range []string{"sha1", "sha256", "sha384", "sha512"} {
			if strings.HasSuffix(r.URL.Path, "."+algo) {
				if !c.publish[algo] {
					http.NotFound(w, r)
					return
				}
				var h hash.Hash
				switch algo {
				case "sha1":
					h = sha1.New()
				case "sha256":
					h = sha256.New()
				case "sha384":
					h = sha512.New384()
				case "sha512":
					h = sha512.New()
				}
				h.Write(c.tarBody)
				fmt.Fprintf(w, "%s  latest.tar.gz\n", hex.EncodeToString(h.Sum(nil)))
				return
			}
		}
		// Bare tarball.
		w.Write(c.tarBody)
	}))
	return c
}

func (c *checksumTestServer) URL() string { return c.srv.URL + "/latest.tar.gz" }
func (c *checksumTestServer) Close()      { c.srv.Close() }

// withCheckumTestHTTPGetFn overrides httpGetFn to route to a fixed
// test server while remembering the original.
func withChecksumTestHTTPGetFn(t *testing.T, baseURL string) {
	t.Helper()
	orig := httpGetFn
	httpGetFn = func(u string) (*http.Response, error) {
		return http.Get(u)
	}
	t.Cleanup(func() { httpGetFn = orig })
	_ = baseURL
}

func TestVerifyWPChecksumPrefersStrongest(t *testing.T) {
	const body = "test wordpress tarball content"
	srv := newChecksumTestServer(body, map[string]bool{
		"sha1":   true,
		"sha256": true,
		"sha384": true,
	})
	defer srv.Close()
	withChecksumTestHTTPGetFn(t, srv.URL())

	// Stage the file locally.
	tarPath := t.TempDir() + "/tar.gz"
	if err := os.WriteFile(tarPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	algo, verified, err := verifyWPChecksum(srv.URL(), tarPath)
	if err != nil {
		t.Fatalf("verifyWPChecksum: %v", err)
	}
	if !verified {
		t.Fatal("expected verified=true")
	}
	if algo != "sha384" {
		t.Errorf("algo = %q, want sha384 (strongest published)", algo)
	}
}

func TestVerifyWPChecksumFallsBackToSHA1(t *testing.T) {
	const body = "legacy mirror content"
	srv := newChecksumTestServer(body, map[string]bool{
		"sha1": true, // only sha1 published
	})
	defer srv.Close()
	withChecksumTestHTTPGetFn(t, srv.URL())

	tarPath := t.TempDir() + "/tar.gz"
	if err := os.WriteFile(tarPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	algo, verified, err := verifyWPChecksum(srv.URL(), tarPath)
	if err != nil {
		t.Fatalf("verifyWPChecksum: %v", err)
	}
	if !verified {
		t.Fatal("expected verified=true")
	}
	if algo != "sha1" {
		t.Errorf("algo = %q, want sha1 (only available)", algo)
	}
}

func TestVerifyWPChecksumDetectsMismatch(t *testing.T) {
	const body = "downloaded content"
	srv := newChecksumTestServer("different content", map[string]bool{
		"sha256": true,
	})
	defer srv.Close()
	withChecksumTestHTTPGetFn(t, srv.URL())

	tarPath := t.TempDir() + "/tar.gz"
	if err := os.WriteFile(tarPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	algo, verified, err := verifyWPChecksum(srv.URL(), tarPath)
	if err == nil {
		t.Fatalf("expected mismatch error, got nil (algo=%q verified=%v)", algo, verified)
	}
	if algo != "sha256" {
		t.Errorf("algo = %q, want sha256", algo)
	}
	if verified {
		t.Error("verified should be false on mismatch")
	}
}

func TestVerifyWPChecksumReturnsUnverifiedIfNoChecksumPublished(t *testing.T) {
	const body = "x"
	srv := newChecksumTestServer(body, map[string]bool{})
	defer srv.Close()
	withChecksumTestHTTPGetFn(t, srv.URL())

	tarPath := t.TempDir() + "/tar.gz"
	if err := os.WriteFile(tarPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	algo, verified, err := verifyWPChecksum(srv.URL(), tarPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verified {
		t.Error("expected verified=false when no checksum published")
	}
	if algo != "" {
		t.Errorf("algo = %q, want empty", algo)
	}
}

// Discard io.ReadAll import if unused in a future refactor.
var _ = io.Discard
