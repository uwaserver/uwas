package wordpress

import (
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

func TestDownloadAndExtractRejectsMissingWordPressDirectory(t *testing.T) {
	snapshot := saveHooks()
	t.Cleanup(func() { restoreHooks(snapshot) })

	httpGetFn = func(url string) (*http.Response, error) {
		status := http.StatusOK
		body := "archive"
		if strings.HasSuffix(url, ".sha1") {
			status = http.StatusNotFound
			body = ""
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}
	// Simulate an extractor that exits successfully but produces an archive
	// layout other than the documented top-level wordpress/ directory.
	execCommandFn = func(string, ...string) *exec.Cmd { return exec.Command("true") }

	err := downloadAndExtract(t.TempDir(), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "missing wordpress directory") {
		t.Fatalf("downloadAndExtract() error = %v, want missing directory error", err)
	}
}
