package wordpress

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type installerDownloadZeros struct{}

func (installerDownloadZeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestDownloadAndExtractRejectsOversizedArchive(t *testing.T) {
	originalGet := httpGetFn
	t.Cleanup(func() { httpGetFn = originalGet })
	httpGetFn = func(string) (*http.Response, error) {
		const over = (100 << 20) + 1
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(io.LimitReader(installerDownloadZeros{}, over)),
		}, nil
	}

	err := downloadAndExtract(t.TempDir(), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "archive exceeds") {
		t.Fatalf("oversized archive error = %v, want archive exceeds", err)
	}
}
