package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestHeaderTransformRequestAdd(t *testing.T) {
	var gotHeader string

	cfg := config.HeadersConfig{
		RequestAdd: map[string]string{
			"X-Custom-Request": "hello",
		},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom-Request")
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if gotHeader != "hello" {
		t.Errorf("X-Custom-Request = %q, want hello", gotHeader)
	}
}

func TestHeaderTransformRequestRemove(t *testing.T) {
	var gotHeader string

	cfg := config.HeadersConfig{
		RequestRemove: []string{"X-Unwanted"},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Unwanted")
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Unwanted", "should-be-gone")
	handler.ServeHTTP(rec, req)

	if gotHeader != "" {
		t.Errorf("X-Unwanted = %q, want empty (removed)", gotHeader)
	}
}

func TestHeaderTransformResponseAdd(t *testing.T) {
	cfg := config.HeadersConfig{
		ResponseAdd: map[string]string{
			"X-Custom-Response": "world",
		},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if got := rec.Header().Get("X-Custom-Response"); got != "world" {
		t.Errorf("X-Custom-Response = %q, want world", got)
	}
}

func TestHeaderTransformResponseRemove(t *testing.T) {
	cfg := config.HeadersConfig{
		ResponseRemove: []string{"X-Powered-By"},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Powered-By", "SomeServer")
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	// The handler sets X-Powered-By before WriteHeader, but our transform
	// wrapper intercepts WriteHeader and removes it.
	if got := rec.Header().Get("X-Powered-By"); got != "" {
		t.Errorf("X-Powered-By = %q, want empty (removed)", got)
	}
}

func TestHeaderTransformVariableSubstitution(t *testing.T) {
	var gotRemoteAddr, gotHost, gotURI, gotRequestID string

	cfg := config.HeadersConfig{
		RequestAdd: map[string]string{
			"X-Client-IP":     "$remote_addr",
			"X-Original-Host": "$host",
			"X-Original-URI":  "$uri",
			"X-Req-ID":        "$request_id",
		},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRemoteAddr = r.Header.Get("X-Client-IP")
		gotHost = r.Header.Get("X-Original-Host")
		gotURI = r.Header.Get("X-Original-URI")
		gotRequestID = r.Header.Get("X-Req-ID")
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/test?q=1", nil)
	req.RemoteAddr = "10.0.0.5:9999"
	req.Host = "example.com"
	req.Header.Set("X-Request-ID", "req-12345")
	handler.ServeHTTP(rec, req)

	if gotRemoteAddr != "10.0.0.5" {
		t.Errorf("$remote_addr = %q, want 10.0.0.5", gotRemoteAddr)
	}
	if gotHost != "example.com" {
		t.Errorf("$host = %q, want example.com", gotHost)
	}
	if gotURI != "/api/test?q=1" {
		t.Errorf("$uri = %q, want /api/test?q=1", gotURI)
	}
	if gotRequestID != "req-12345" {
		t.Errorf("$request_id = %q, want req-12345", gotRequestID)
	}
}

func TestHeaderTransformResponseVariableSubstitution(t *testing.T) {
	cfg := config.HeadersConfig{
		ResponseAdd: map[string]string{
			"X-Served-For": "$remote_addr",
			"X-Served-Host": "$host",
		},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:8080"
	req.Host = "myapp.com"
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Served-For"); got != "192.168.1.1" {
		t.Errorf("X-Served-For = %q, want 192.168.1.1", got)
	}
	if got := rec.Header().Get("X-Served-Host"); got != "myapp.com" {
		t.Errorf("X-Served-Host = %q, want myapp.com", got)
	}
}

func TestHeaderTransformEmptyConfig(t *testing.T) {
	cfg := config.HeadersConfig{}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", rec.Body.String())
	}
}

func TestHeaderTransformWriteWithoutExplicitWriteHeader(t *testing.T) {
	cfg := config.HeadersConfig{
		ResponseAdd: map[string]string{
			"X-Auto": "true",
		},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write without calling WriteHeader first
		w.Write([]byte("auto header"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if got := rec.Header().Get("X-Auto"); got != "true" {
		t.Errorf("X-Auto = %q, want true (should be set even without explicit WriteHeader)", got)
	}
}

func TestHeaderTransformCombined(t *testing.T) {
	var gotReqHeader string

	cfg := config.HeadersConfig{
		RequestAdd: map[string]string{
			"X-Added-Req": "yes",
		},
		RequestRemove: []string{"X-Remove-Req"},
		ResponseAdd: map[string]string{
			"X-Added-Resp": "yes",
		},
		ResponseRemove: []string{"X-Remove-Resp"},
	}

	handler := HeaderTransform(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReqHeader = r.Header.Get("X-Added-Req")
		w.Header().Set("X-Remove-Resp", "should-be-removed")
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Remove-Req", "should-be-removed")
	handler.ServeHTTP(rec, req)

	if gotReqHeader != "yes" {
		t.Errorf("X-Added-Req = %q, want yes", gotReqHeader)
	}
	if got := rec.Header().Get("X-Added-Resp"); got != "yes" {
		t.Errorf("X-Added-Resp = %q, want yes", got)
	}
	if got := rec.Header().Get("X-Remove-Resp"); got != "" {
		t.Errorf("X-Remove-Resp = %q, want empty", got)
	}
}

func TestSubstituteVarsNoVars(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	result := substituteVars("plain value", req)
	if result != "plain value" {
		t.Errorf("got %q, want 'plain value'", result)
	}
}

func TestSubstituteVarsMultipleVars(t *testing.T) {
	req := httptest.NewRequest("GET", "/path?q=1", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Host = "test.com"
	req.Header.Set("X-Request-ID", "abc")

	result := substituteVars("ip=$remote_addr host=$host uri=$uri id=$request_id", req)
	expected := "ip=1.2.3.4 host=test.com uri=/path?q=1 id=abc"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractRemoteAddrWithPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:8080"
	got := extractRemoteAddr(req)
	if got != "10.0.0.1" {
		t.Errorf("got %q, want 10.0.0.1", got)
	}
}

func TestExtractRemoteAddrWithoutPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1"
	got := extractRemoteAddr(req)
	if got != "10.0.0.1" {
		t.Errorf("got %q, want 10.0.0.1", got)
	}
}
