package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestGracefulRestart_NoListeners(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 5 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := &Server{
		config: cfg,
		logger: log,
	}

	// GracefulRestart should succeed when no listeners are active
	if err := s.GracefulRestart(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGracefulRestart_WithHTTPServer(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 5 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	}

	s := &Server{
		config:  cfg,
		logger:  log,
		httpSrv: srv,
	}

	// Should succeed even with an unstarted server (no listeners to close)
	err := s.GracefulRestart()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDrainAndWait_NoListeners(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := &Server{
		config: cfg,
		logger: log,
	}

	// Should complete quickly with no listeners
	done := make(chan struct{})
	go func() {
		s.DrainAndWait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("DrainAndWait did not complete in time")
	}
}

func TestDrainAndWait_DefaultGrace(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				// Zero grace - should default to 30s
			},
		},
	}
	log := logger.New("error", "text")
	s := &Server{
		config: cfg,
		logger: log,
	}

	done := make(chan struct{})
	go func() {
		s.DrainAndWait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("DrainAndWait did not complete in time")
	}
}
