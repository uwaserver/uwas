package admin

// F39200: removing a file and replacing an existing one through the file
// manager left no audit record, while every other destructive admin action
// (database drop, backup delete, user delete) has one. Routine edits are NOT
// audited on purpose - creating files, making directories and saving a new
// file would flood the 500-entry audit ring and push out the entries that
// matter - so the test pins both sides.

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileAuditRequest(s *Server, method, path string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	if body == nil {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func fileAuditUploadBody(t *testing.T, name, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte(content))
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestFileDeleteIsAudited(t *testing.T) {
	s, root := testServerWithRoot(t)
	os.WriteFile(filepath.Join(root, "old.txt"), []byte("x"), 0o644)

	rec := fileAuditRequest(s, "DELETE", "/api/v1/files/example.com/delete?path=old.txt", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
	t.Logf("EXPECTED: audit file.delete|true\nACTUAL: %v", auditActions(s))
	if !hasAudit(s, "file.delete|true") {
		t.Fatal("PROBLEM CONFIRMED: deleting a file left no audit record")
	}

	// An escaping path is refused by the file manager; the attempt is audited.
	rec = fileAuditRequest(s, "DELETE", "/api/v1/files/example.com/delete?path=../escape", nil, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete of an escaping path = %d, want 400", rec.Code)
	}
	if !hasAudit(s, "file.delete|false") {
		t.Fatalf("a failed delete must be audited too: %v", auditActions(s))
	}
}

func TestFileOverwriteIsAuditedButRoutineEditsAreNot(t *testing.T) {
	s, root := testServerWithRoot(t)

	// New file via the editor and via upload: routine, not audited.
	if rec := fileAuditRequest(s, "PUT", "/api/v1/files/example.com/write", bytes.NewBufferString(`{"path":"new.txt","content":"one"}`), "application/json"); rec.Code != 200 {
		t.Fatalf("write new = %d: %s", rec.Code, rec.Body.String())
	}
	body, ct := fileAuditUploadBody(t, "fresh.txt", "one")
	if rec := fileAuditRequest(s, "POST", "/api/v1/files/example.com/upload", body, ct); rec.Code != 200 {
		t.Fatalf("upload new = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := fileAuditRequest(s, "POST", "/api/v1/files/example.com/mkdir", bytes.NewBufferString(`{"path":"dir"}`), "application/json"); rec.Code != 200 {
		t.Fatalf("mkdir = %d: %s", rec.Code, rec.Body.String())
	}
	for _, a := range auditActions(s) {
		if strings.HasPrefix(a, "file.") {
			t.Fatalf("control: routine create/mkdir was audited as %q", a)
		}
	}

	// Replacing existing content through either path is audited.
	if rec := fileAuditRequest(s, "PUT", "/api/v1/files/example.com/write", bytes.NewBufferString(`{"path":"new.txt","content":"two"}`), "application/json"); rec.Code != 200 {
		t.Fatalf("overwrite via write = %d: %s", rec.Code, rec.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(root, "new.txt"))
	if string(data) != "two" {
		t.Fatalf("overwrite did not take effect: %q", data)
	}
	t.Logf("EXPECTED: audit file.overwrite|true after write\nACTUAL: %v", auditActions(s))
	if !hasAudit(s, "file.overwrite|true") {
		t.Fatal("PROBLEM CONFIRMED: replacing an existing file via write left no audit record")
	}

	before := fileAuditCount(s, "file.overwrite|true")
	body, ct = fileAuditUploadBody(t, "fresh.txt", "two")
	if rec := fileAuditRequest(s, "POST", "/api/v1/files/example.com/upload", body, ct); rec.Code != 200 {
		t.Fatalf("overwrite via upload = %d: %s", rec.Code, rec.Body.String())
	}
	if got := fileAuditCount(s, "file.overwrite|true"); got != before+1 {
		t.Fatalf("overwrite via upload: %d overwrite records, want %d", got, before+1)
	}
}

func fileAuditCount(s *Server, want string) int {
	n := 0
	for _, a := range auditActions(s) {
		if a == want {
			n++
		}
	}
	return n
}
