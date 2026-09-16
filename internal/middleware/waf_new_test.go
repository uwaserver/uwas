package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// --- DomainWAF: Content-Type skip tests ---

// JSON body with no attack pattern: passes.
func TestDomainWAFJSONBodySafe(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := `{"query": "SELECT * FROM users WHERE id = 1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("safe JSON body: status = %d, want 200", rec.Code)
	}
}

// JSON body with a WAF-pattern payload is now blocked (was a bypass).
func TestDomainWAFJSONBodyBlocked(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := `{"query": "1' UNION SELECT username,password FROM users--"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("JSON body with SQLi: status = %d, want 403 (WAF must scan JSON values)", rec.Code)
	}
}

func TestDomainWAFJSONCharsetNotScanned(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := `{"script": "<script>alert(1)</script>"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/data", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("JSON with charset: status = %d, want 200", rec.Code)
	}
}

// Multipart with no attack pattern: passes.
func TestDomainWAFMultipartSafe(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := "------WebKitFormBoundary\r\nContent-Disposition: form-data; name=\"q\"\r\n\r\nhello world\r\n------WebKitFormBoundary--"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/upload", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=----WebKitFormBoundary")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("safe multipart body: status = %d, want 200", rec.Code)
	}
}

// Multipart with a WAF-pattern payload is now blocked (was a bypass).
func TestDomainWAFMultipartBlocked(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := "------WebKitFormBoundary\r\nContent-Disposition: form-data; name=\"q\"\r\n\r\n1' UNION SELECT username,password FROM users--\r\n------WebKitFormBoundary--"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/upload", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=----WebKitFormBoundary")
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("multipart body with SQLi: status = %d, want 403 (WAF must scan form fields)", rec.Code)
	}
}

func TestDomainWAFXMLNotScanned(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := "<?xml version=\"1.0\"?><data><query>UNION SELECT * FROM users</query></data>"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("XML body: status = %d, want 200", rec.Code)
	}
}

func TestDomainWAFGraphQLJSONNotScanned(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := `{"query": "{ users { id name } }"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/graphql+json")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GraphQL JSON body: status = %d, want 200", rec.Code)
	}
}

func TestDomainWAFFormBodyStillScanned(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := "username=admin' UNION SELECT * FROM users--"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("form body with SQLi: status = %d, want 403", rec.Code)
	}
}

func TestDomainWAFNoContentTypeStillScanned(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := "data=javascript:alert(1)"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/form", strings.NewReader(body))
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("no content-type body with XSS: status = %d, want 403", rec.Code)
	}
}

// --- DomainWAF: Bypass paths ---

func TestDomainWAFBypassPath(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, []string{"/api/webhooks/", "/api/stripe/"}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/webhooks/stripe?data=UNION+SELECT+*+FROM+users", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("bypass path: status = %d, want 200", rec.Code)
	}
}

func TestDomainWAFBypassPathNotMatched(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, []string{"/api/webhooks/"}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/users?query=UNION+SELECT+*+FROM+users", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("non-bypass path: status = %d, want 403", rec.Code)
	}
}

// --- isAPContentType tests ---

func TestIsAPContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		// application/json and multipart/form-data are now scanned, so they return false.
		{"application/json", false},
		{"application/json; charset=utf-8", false},
		{"APPLICATION/JSON", false},
		{"multipart/form-data; boundary=something", false},
		// +json suffix types are scanned.
		{"application/vnd.api+json", false},
		{"application/protobuf+json", false},
		// These types remain skipped.
		{"application/xml", true},
		{"text/xml", true},
		{"application/soap+xml", true},
		{"application/graphql+json", true},
		{"application/grpc", true},
		{"application/grpc-web", true},
		{"application/octet-stream", true},
		{"application/x-protobuf", true},
		// +xml suffix types are skipped.
		{"application/soap+xml", true},
		// Not skipped.
		{"application/x-www-form-urlencoded", false},
		{"text/html", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isAPContentType(tt.ct)
		if got != tt.want {
			t.Errorf("isAPContentType(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

// --- Script tag no longer blocked in body ---

func TestDomainWAFScriptTagAllowedInBody(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := "<h1>Title</h1><script>console.log(\"hello\")</script><p>Content</p>"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/wp-admin/post.php", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("CMS script tag in body: status = %d, want 200", rec.Code)
	}
}

func TestDomainWAFSleepAllowedInBody(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := "code=function test() { sleep(100); return true; }"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/run", strings.NewReader(body))
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("sleep() in body: status = %d, want 200", rec.Code)
	}
}

func TestDomainWAFScriptInURLStillBlocked(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page?q=<script>alert(1)</script>", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("<script> in URL: status = %d, want 403", rec.Code)
	}
}

func TestDomainWAFSleepInURLStillBlocked(t *testing.T) {
	log := logger.New("error", "text")

	handler := DomainWAF(log, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page?cmd=sleep(5)", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("sleep() in URL: status = %d, want 403", rec.Code)
	}
}
