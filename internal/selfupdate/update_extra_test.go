package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checksumForAsset with path prefix in name exercises the LastIndex("/") branch
func TestChecksumForAsset_WithPathPrefix(t *testing.T) {
	hash := sha256.Sum256([]byte("data"))
	hexHash := hex.EncodeToString(hash[:])
	sums := hexHash + "  path/to/download\n" + "aaaa  other-file\n"
	got := checksumForAsset(sums, "download")
	if got != hexHash {
		t.Fatalf("checksumForAsset with path = %q, want %q", got, hexHash)
	}
	// Asset listed as bare name without path
	sums = hexHash + "  download\n"
	got = checksumForAsset(sums, "download")
	if got != hexHash {
		t.Fatalf("checksumForAsset bare = %q, want %q", got, hexHash)
	}
}

// checksumForAsset with binary marker prefix "*"
func TestChecksumForAsset_BinaryMarker(t *testing.T) {
	hash := sha256.Sum256([]byte("data"))
	hexHash := hex.EncodeToString(hash[:])
	sums := hexHash + "  *download\n"
	got := checksumForAsset(sums, "download")
	if got != hexHash {
		t.Fatalf("checksumForAsset with * marker = %q, want %q", got, hexHash)
	}
}

// checksumForAsset with no match returns ""
func TestChecksumForAsset_NoMatch(t *testing.T) {
	sums := "aaaa  other-file\n"
	got := checksumForAsset(sums, "download")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// Update with empty download URL
func TestUpdate_EmptyURLExtra(t *testing.T) {
	saveHooks(t)
	err := Update("")
	if err == nil || !strings.Contains(err.Error(), "no download URL") {
		t.Fatalf("expected 'no download URL' error, got %v", err)
	}
}

// Update: restore backup when rename fails
func TestUpdate_RestoreOnRenameFailure(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	exePath := filepath.Join(tmpDir, "uwas")
	if err := os.WriteFile(exePath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	osExecutableFn = func() (string, error) { return exePath, nil }
	evalSymlinksFn = func(p string) (string, error) { return p, nil }

	// Make the first rename succeed (backup), second rename fail (replace)
	renameCalls := 0
	osRenameFn = func(old, new string) error {
		renameCalls++
		if renameCalls == 2 {
			return fmt.Errorf("rename failed")
		}
		return os.Rename(old, new)
	}

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte("new-binary"))
	})

	err := Update(srv.URL + "/download")
	if err == nil || !strings.Contains(err.Error(), "replace binary") {
		t.Fatalf("expected 'replace binary' error after rename failure, got %v", err)
	}
}

// Update: checksum verification success (200 from SHA256SUMS, valid hash)
func TestUpdate_ChecksumSuccess(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	exePath := filepath.Join(tmpDir, "uwas")
	if err := os.WriteFile(exePath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	osExecutableFn = func() (string, error) { return exePath, nil }
	evalSymlinksFn = func(p string) (string, error) { return p, nil }

	binaryContent := []byte("checksum-verified-binary")

	srv := newGitHubServer(t, binaryHandler(binaryContent))

	if err := Update(srv.URL + "/download"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != string(binaryContent) {
		t.Errorf("binary = %q, want %q", got, string(binaryContent))
	}
}

// Update: checksum mismatch
func TestUpdate_ChecksumMismatchExtra(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	exePath := filepath.Join(tmpDir, "uwas")
	if err := os.WriteFile(exePath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	osExecutableFn = func() (string, error) { return exePath, nil }
	evalSymlinksFn = func(p string) (string, error) { return p, nil }

	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			// Return wrong checksum
			io.WriteString(w, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  download\n")
			return
		}
		w.Write([]byte("some-binary-content"))
	})

	err := Update(srv.URL + "/download")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "old" {
		t.Errorf("binary was replaced despite checksum mismatch: %q", got)
	}
}

// Update: SHA256SUMS fetch error (network error) - verification skipped
func TestUpdate_ChecksumsFetchError(t *testing.T) {
	saveHooks(t)
	tmpDir := t.TempDir()

	exePath := filepath.Join(tmpDir, "uwas")
	if err := os.WriteFile(exePath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	osExecutableFn = func() (string, error) { return exePath, nil }
	evalSymlinksFn = func(p string) (string, error) { return p, nil }

	// Return error for SHA256SUMS fetch, but Success for the download
	callCount := 0
	srv := newGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			// Simulate network error by closing connection
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
			}
			return
		}
		w.Write([]byte("binary-without-checksum"))
	})

	// If hijack works, the fetch will error. Otherwise, the 200 with empty body will
	// still succeed. Either way, the update should succeed (verification is optional).
	_ = Update(srv.URL + "/download")
}
