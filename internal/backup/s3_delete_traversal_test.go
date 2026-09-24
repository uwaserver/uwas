package backup

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestS3Delete_MissingSafeBackupFilename verifies that S3Provider.Delete calls
// safeBackupFilename before constructing the S3 key URL. Without this guard, an
// admin who can call DELETE /api/backup/:name?provider=s3 with a path-traversal
// key name can cause the S3 provider to issue a signed DELETE request for an
// arbitrary S3 key (object-write equivalent of SSRF).
//
// safeBackupFilename is called by Upload and Download but was NOT called by Delete.
func TestS3Delete_MissingSafeBackupFilename(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Provide a DialContext so the HTTP client can reach the http:// mock server.
	dialer := &net.Dialer{}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}
	p := &S3Provider{
		endpoint:  srv.URL,
		bucket:    "uwas-backups",
		accessKey: "testkey",
		secretKey: "testsecret",
		region:    "us-east-1",
		client:    client,
	}

	// A path-traversal key name. safeBackupFilename rejects keys containing "..",
	// "/", or "\". If Delete calls safeBackupFilename, it returns an error before
	// any HTTP request is made. Without it, the raw key is embedded in the URL.
	traversalKey := "../../../other-bucket/secret-key"
	err := p.Delete(context.Background(), traversalKey)

	// The fix makes Delete return an error for traversal keys.
	// Before the fix: err == nil, receivedPath == "/other-bucket/secret-key" (normalised).
	// After  the fix: err != nil, receivedPath == "" (no request sent).
	if err == nil {
		t.Errorf("Delete should have rejected traversal key %q, but it sent request to path %q",
			traversalKey, receivedPath)
	}
	if !strings.Contains(err.Error(), "invalid backup filename") {
		t.Errorf("Delete error should mention 'invalid backup filename', got: %v", err)
	}
	if receivedPath != "" {
		t.Errorf("mock S3 server should not have received any request, but got: %s", receivedPath)
	}
}
