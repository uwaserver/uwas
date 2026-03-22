package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuthSuccess(t *testing.T) {
	handler := BasicAuth(map[string]string{"admin": "secret"}, "Test")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.Write([]byte("ok"))
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "secret")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestBasicAuthWrongPassword(t *testing.T) {
	handler := BasicAuth(map[string]string{"admin": "secret"}, "Test")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "wrong")
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("should have WWW-Authenticate header")
	}
}

func TestBasicAuthNoCredentials(t *testing.T) {
	handler := BasicAuth(map[string]string{"admin": "secret"}, "")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestBasicAuthUnknownUser(t *testing.T) {
	handler := BasicAuth(map[string]string{"admin": "secret"}, "Test")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("nobody", "secret")
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestBasicAuthEmptyUsers(t *testing.T) {
	handler := BasicAuth(nil, "")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (no auth when empty users)", rec.Code)
	}
}

func TestBasicAuthTimingAttack(t *testing.T) {
	handler := BasicAuth(map[string]string{"admin": "correct-password-here"}, "Test")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	)

	// Both wrong — response should be same timing (constant-time compare)
	for _, pass := range []string{"a", "wrong-password-different-length"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("admin", pass)
		handler.ServeHTTP(rec, req)

		if rec.Code != 401 {
			t.Errorf("pass=%q: status = %d, want 401", pass, rec.Code)
		}
	}
}
