package server

import (
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestNewServerCov(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)
	if s == nil {
		t.Fatal("New returned nil")
	}
}

func TestSafeHeaderValueCov(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello\nworld", "helloworld"},
		{"hello\rworld", "helloworld"},
		{"", ""},
	}
	for _, tc := range tests {
		got := safeHeaderValue(tc.input)
		if got != tc.expected {
			t.Errorf("safeHeaderValue(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestRenderDomainErrorCov(t *testing.T) {
	rec := httptest.NewRecorder()
	renderDomainError(rec, 502, nil)
	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestNewProxyProtoListenerFunc(t *testing.T) {
	_ = newProxyProtoListener
}
