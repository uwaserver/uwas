package backup

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

type s3ErrorBodyRT struct{ body *countingErrorBody }

func (rt s3ErrorBodyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Status:     "500 Internal Server Error",
		Header:     make(http.Header),
		Body:       rt.body,
		Request:    req,
	}, nil
}

type countingErrorBody struct {
	io.Reader
	read int
}

func (b *countingErrorBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (*countingErrorBody) Close() error { return nil }

func TestS3DownloadBoundsErrorResponseDrain(t *testing.T) {
	for _, size := range []int{128, 1 << 20} {
		body := &countingErrorBody{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), size))}
		p := NewS3Provider("https://s3.example.com", "bucket", "key", "secret", "us-east-1")
		p.client = &http.Client{Transport: s3ErrorBodyRT{body: body}}

		if _, err := p.Download(context.Background(), "backup.tar.gz"); err == nil {
			t.Fatalf("size %d: expected status error", size)
		}
		wantMax := size
		if wantMax > 4096 {
			wantMax = 4096
		}
		if body.read > wantMax {
			t.Fatalf("size %d: drained %d error bytes, want at most %d", size, body.read, wantMax)
		}
	}
}
