package backup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// F58002: List decoded the ListObjectsV2 reply with no size cap, so an
// endpoint that streams an endless (or merely enormous) listing was read to
// the end into memory. A page of up to 1000 keys is well under the cap.
func TestS3ListResponseIsBounded(t *testing.T) {
	const chunk = 1 << 20
	const chunks = 48
	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<ListBucketResult>"))
		entry := strings.Repeat("<Contents><Key>uwas-x.tar.gz</Key><Size>1</Size></Contents>", chunk/62)
		for i := 0; i < chunks; i++ {
			if _, err := w.Write([]byte(entry)); err != nil {
				return
			}
			served.Add(int64(len(entry)))
		}
	}))
	defer srv.Close()
	p := &S3Provider{endpoint: srv.URL, bucket: "b", accessKey: "k", secretKey: "s", region: "us-east-1", client: &http.Client{}}
	if _, err := p.List(context.Background()); err == nil {
		t.Fatal("EXPECTED: error for an oversized listing; ACTUAL: nil")
	}
	if got := served.Load(); got > 40<<20 {
		t.Fatalf("EXPECTED: read stops near the cap; ACTUAL: server delivered %d MiB", got>>20)
	}
}

// Control: a normal listing still parses.
func TestS3ListNormalResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<ListBucketResult><Contents><Key>uwas-backup-1.tar.gz</Key><Size>5</Size><LastModified>2026-01-02T03:04:05Z</LastModified></Contents></ListBucketResult>`))
	}))
	defer srv.Close()
	p := &S3Provider{endpoint: srv.URL, bucket: "b", accessKey: "k", secretKey: "s", region: "us-east-1", client: &http.Client{}}
	infos, err := p.List(context.Background())
	if err != nil || len(infos) != 1 || infos[0].Name != "uwas-backup-1.tar.gz" {
		t.Fatalf("infos=%v err=%v", infos, err)
	}
}
