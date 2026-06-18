package admin

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// =============================================================================
// Pure helpers: appSFTPIdentity / appSFTPTargetName / appFileTargetName
// =============================================================================

func TestGrpF_AppSFTPIdentity(t *testing.T) {
	got := appSFTPIdentity("My_App")
	if got != "app-my--u--app.uwas.local" {
		t.Fatalf("appSFTPIdentity = %q", got)
	}
}

func TestGrpF_AppSFTPTargetName(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"app-myapp.uwas.local", "myapp", true},
		{"app-my--u--app.uwas.local", "my_app", true},
		{"example.com", "", false},
		{"app-.uwas.local", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := appSFTPTargetName(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("appSFTPTargetName(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// =============================================================================
// requireJSONMiddleware
// =============================================================================

func TestGrpF_RequireJSONMiddleware(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) })
	mw := requireJSONMiddleware(next)

	// GET passes through regardless of content type
	called = false
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/x", nil))
	if !called || rec.Code != 200 {
		t.Errorf("GET should pass: called=%v code=%d", called, rec.Code)
	}

	// POST without JSON content type -> 415
	called = false
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/x", strings.NewReader("{}")))
	if called || rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST non-json should 415: called=%v code=%d", called, rec.Code)
	}

	// POST with JSON content type -> passes
	called = false
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/x", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	mw.ServeHTTP(rec, req)
	if !called || rec.Code != 200 {
		t.Errorf("POST json should pass: called=%v code=%d", called, rec.Code)
	}

	// POST to /upload path is exempt
	called = false
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/files/x/upload", strings.NewReader("data")))
	if !called {
		t.Error("POST /upload should be exempt")
	}

	// POST to /import path is exempt
	called = false
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/db/import", strings.NewReader("data")))
	if !called {
		t.Error("POST /import should be exempt")
	}
}

// =============================================================================
// jsonErrorCause
// =============================================================================

func TestGrpF_JSONErrorCause(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonErrorCause(rec, "boom", errors.New("underlying"), http.StatusInternalServerError)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "boom") {
		t.Errorf("body missing message: %s", body)
	}
	// underlying cause must NOT be serialized to the client
	if strings.Contains(body, "underlying") {
		t.Errorf("cause leaked to client: %s", body)
	}
}

// =============================================================================
// parsePagination
// =============================================================================

func TestGrpF_ParsePagination(t *testing.T) {
	cases := []struct {
		query              string
		wantLimit, wantOff int
	}{
		{"", 50, 0},
		{"limit=10&offset=5", 10, 5},
		{"limit=99999", 500, 0},       // clamp to max
		{"limit=-1&offset=-1", 50, 0}, // invalid -> defaults
		{"limit=abc&offset=xyz", 50, 0},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/?"+c.query, nil)
		l, o := parsePagination(r)
		if l != c.wantLimit || o != c.wantOff {
			t.Errorf("parsePagination(%q) = (%d,%d), want (%d,%d)", c.query, l, o, c.wantLimit, c.wantOff)
		}
	}
}

// =============================================================================
// PHP domain handlers: nil-manager NotImplemented branches + reseller gating
// =============================================================================

func TestGrpF_PHPHandlersNilManager(t *testing.T) {
	s := testServer() // phpMgr is nil
	handlers := map[string]func(http.ResponseWriter, *http.Request){
		"assign":   s.handlePHPDomainAssign,
		"start":    s.handlePHPDomainStart,
		"stop":     s.handlePHPDomainStop,
		"unassign": s.handlePHPDomainUnassign,
	}
	for name, h := range handlers {
		rec := httptest.NewRecorder()
		req := withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{}")))
		h(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s nil-manager = %d, want 501", name, rec.Code)
		}
	}
}

func TestGrpF_PHPDomainAssignValidation(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	// invalid JSON
	rec := httptest.NewRecorder()
	s.handlePHPDomainAssign(rec, withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{bad"))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}

	// missing domain
	rec = httptest.NewRecorder()
	s.handlePHPDomainAssign(rec, withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader(`{"version":"8.3"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing domain = %d, want 400", rec.Code)
	}

	// missing version
	rec = httptest.NewRecorder()
	s.handlePHPDomainAssign(rec, withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader(`{"domain":"example.com"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing version = %d, want 400", rec.Code)
	}
}

func TestGrpF_PHPDomainStartStopResellerForbidden(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))
	s.authMgr = newMockAuthManager()

	for _, h := range []func(http.ResponseWriter, *http.Request){s.handlePHPDomainStart, s.handlePHPDomainStop} {
		rec := httptest.NewRecorder()
		req := withResellerContext(httptest.NewRequest("POST", "/x", nil))
		req.SetPathValue("domain", "example.com") // not in reseller's domains
		h(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("reseller forbidden = %d, want 403", rec.Code)
		}
	}
}

// =============================================================================
// File handlers
// =============================================================================

func TestGrpF_FileWriteReadDeleteCycle(t *testing.T) {
	s, root := testServerWithRoot(t)
	_ = root

	// write a text file
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader(`{"path":"hello.txt","content":"hi there"}`)))
	req.SetPathValue("domain", "example.com")
	s.handleFileWrite(rec, req)
	if rec.Code != 200 {
		t.Fatalf("write = %d body=%s", rec.Code, rec.Body.String())
	}

	// read it back (text -> JSON)
	rec = httptest.NewRecorder()
	req = withAdminContext(httptest.NewRequest("GET", "/x?path=hello.txt", nil))
	req.SetPathValue("domain", "example.com")
	s.handleFileRead(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hi there") {
		t.Fatalf("read = %d body=%s", rec.Code, rec.Body.String())
	}

	// delete
	rec = httptest.NewRecorder()
	req = withAdminContext(httptest.NewRequest("DELETE", "/x?path=hello.txt", nil))
	req.SetPathValue("domain", "example.com")
	s.handleFileDelete(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGrpF_FileReadImageBinary(t *testing.T) {
	s, root := testServerWithRoot(t)
	// write a fake png directly to disk
	if err := os.WriteFile(filepath.Join(root, "pic.png"), []byte("\x89PNGDATA"), 0644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("GET", "/x?path=pic.png", nil))
	req.SetPathValue("domain", "example.com")
	s.handleFileRead(rec, req)
	if rec.Code != 200 {
		t.Fatalf("read png = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if rec.Body.String() != "\x89PNGDATA" {
		t.Errorf("binary body mismatch")
	}
}

func TestGrpF_FileReadInvalidPath(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("GET", "/x?path=../../../etc/passwd", nil))
	req.SetPathValue("domain", "example.com")
	s.handleFileRead(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("traversal read = %d, want 400", rec.Code)
	}
}

func TestGrpF_FileMkdirAndList(t *testing.T) {
	s, _ := testServerWithRoot(t)

	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader(`{"path":"subdir"}`)))
	req.SetPathValue("domain", "example.com")
	s.handleFileMkdir(rec, req)
	if rec.Code != 200 {
		t.Fatalf("mkdir = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = withAdminContext(httptest.NewRequest("GET", "/x?path=.", nil))
	req.SetPathValue("domain", "example.com")
	s.handleFileList(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "subdir") {
		t.Fatalf("list = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGrpF_FileWriteInvalidJSON(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{bad")))
	req.SetPathValue("domain", "example.com")
	s.handleFileWrite(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}
}

func TestGrpF_FileUploadMultipart(t *testing.T) {
	s, _ := testServerWithRoot(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("path", ".")
	fw, _ := mw.CreateFormFile("file", "upload.txt")
	fw.Write([]byte("uploaded content"))
	mw.Close()

	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", &buf))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("domain", "example.com")
	s.handleFileUpload(rec, req)
	if rec.Code != 200 {
		t.Fatalf("upload = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upload.txt") {
		t.Errorf("upload response missing filename: %s", rec.Body.String())
	}
}

func TestGrpF_FileUploadBadMultipart(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("not multipart")))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=nope")
	req.SetPathValue("domain", "example.com")
	s.handleFileUpload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad multipart = %d, want 400", rec.Code)
	}
}

func TestGrpF_FileHandlersResellerForbidden(t *testing.T) {
	s, _ := testServerWithRoot(t)
	s.authMgr = newMockAuthManager()

	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("GET", "/x?path=.", nil))
	req.SetPathValue("domain", "example.com") // not owned by reseller
	s.handleFileList(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller file list = %d, want 403", rec.Code)
	}
}

func TestGrpF_DiskUsage(t *testing.T) {
	s, root := testServerWithRoot(t)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("12345"), 0644)
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("GET", "/x", nil))
	req.SetPathValue("domain", "example.com")
	s.handleDiskUsage(rec, req)
	if rec.Code != 200 {
		t.Fatalf("disk usage = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bytes") {
		t.Errorf("disk usage body: %s", rec.Body.String())
	}
}

// =============================================================================
// siteUserRoot for an app target
// =============================================================================

func TestGrpF_SiteUserRootDomain(t *testing.T) {
	s, root := testServerWithRoot(t)
	got, err := s.siteUserRoot("example.com")
	if err != nil {
		t.Fatalf("siteUserRoot err: %v", err)
	}
	if got != root {
		t.Errorf("siteUserRoot = %q, want %q", got, root)
	}
}
