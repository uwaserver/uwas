package admin

// F35303: a multipart upload that carries no file parts answered
// {"status":"uploaded","files":null}. The list of saved files must be an
// array, and a normal upload still lists what it saved.

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

func uploadBody(t *testing.T, withFile bool) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("path", "."); err != nil {
		t.Fatal(err)
	}
	if withFile {
		fw, err := mw.CreateFormFile("file", "a.txt")
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte("hello"))
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestFileUploadFilesListIsArray(t *testing.T) {
	for _, c := range []struct {
		name     string
		withFile bool
		want     string
	}{
		{"no file parts", false, `"files":[]`},
		{"control: one file", true, `"files":["a.txt"]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := testServerWithRoot(t)
			body, ct := uploadBody(t, c.withFile)
			req := withAdminContext(httptest.NewRequest("POST", "/api/v1/files/example.com/upload", body))
			req.Header.Set("Content-Type", ct)
			req.SetPathValue("domain", "example.com")
			rec := httptest.NewRecorder()
			s.handleFileUpload(rec, req)
			got := strings.TrimSpace(rec.Body.String())
			t.Logf("EXPECTED: %s\nACTUAL: %s", c.want, got)
			if rec.Code != 200 || !strings.Contains(got, c.want) {
				t.Fatalf("PROBLEM CONFIRMED: status %d body %s, want %s", rec.Code, got, c.want)
			}
		})
	}
}
