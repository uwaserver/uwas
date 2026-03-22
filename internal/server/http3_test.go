package server

import (
	"testing"

	"github.com/quic-go/quic-go/http3"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestAltSvcHeaderEnabled(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPSListen:  ":443",
			HTTP3Enabled: true,
			LogLevel:     "error",
			LogFormat:    "text",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Simulate h3srv being set (normally done by startHTTP3)
	s.h3srv = &http3.Server{}

	alt := s.altSvcHeader()
	if alt == "" {
		t.Error("altSvcHeader should return a value when HTTP/3 is enabled")
	}
	if alt != `h3=":443"; ma=86400` {
		t.Errorf("altSvcHeader = %q, want h3=\":443\"; ma=86400", alt)
	}
}

func TestAltSvcHeaderCustomPort(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPSListen:  "0.0.0.0:8443",
			HTTP3Enabled: true,
			LogLevel:     "error",
			LogFormat:    "text",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)
	s.h3srv = &http3.Server{}

	alt := s.altSvcHeader()
	if alt != `h3=":8443"; ma=86400` {
		t.Errorf("altSvcHeader = %q, want h3=\":8443\"; ma=86400", alt)
	}
}

func TestAltSvcHeaderDisabled(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTP3Enabled: false,
			LogLevel:     "error",
			LogFormat:    "text",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	alt := s.altSvcHeader()
	if alt != "" {
		t.Errorf("altSvcHeader should be empty when HTTP/3 is disabled, got %q", alt)
	}
}

func TestAltSvcHeaderNoH3Server(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTP3Enabled: true,
			LogLevel:     "error",
			LogFormat:    "text",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)
	// h3srv is nil

	alt := s.altSvcHeader()
	if alt != "" {
		t.Errorf("altSvcHeader should be empty when h3srv is nil, got %q", alt)
	}
}

func TestHTTP3Config(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTP3Enabled: true,
			HTTPSListen:  ":443",
			LogLevel:     "error",
			LogFormat:    "text",
		},
	}

	if !cfg.Global.HTTP3Enabled {
		t.Error("HTTP3Enabled should be true")
	}
}
