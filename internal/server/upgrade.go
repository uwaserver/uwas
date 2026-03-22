package server

import (
	"context"
	"fmt"
	"time"
)

// GracefulRestart performs a graceful restart by:
// 1. Stopping acceptance of new connections
// 2. Draining in-flight connections up to the shutdown grace timeout
// 3. Returning so the caller can start a new process
//
// This is the v1.0 simple approach: the CLI "uwas restart" command sends
// SIGTERM (handled by handleSignals), waits for the process to exit, then
// starts a new process. The systemd unit handles this via ExecReload.
func (s *Server) GracefulRestart() error {
	s.logger.Info("graceful restart: draining connections")

	grace := s.config.Global.Timeouts.ShutdownGrace.Duration
	if grace == 0 {
		grace = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	var errs []error

	// Stop accepting new connections on HTTP
	if s.httpSrv != nil {
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		}
	}

	// Stop accepting new connections on HTTPS
	if s.httpsSrv != nil {
		if err := s.httpsSrv.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("https shutdown: %w", err))
		}
	}

	// Stop admin API
	if s.admin != nil && s.admin.HTTPServer() != nil {
		if err := s.admin.HTTPServer().Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("admin shutdown: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("graceful restart errors: %v", errs)
	}

	s.logger.Info("graceful restart: all connections drained")
	return nil
}

// DrainAndWait stops accepting new connections and waits for all in-flight
// requests to complete, up to the configured shutdown grace period.
// This is called during normal shutdown and during upgrade.
func (s *Server) DrainAndWait() {
	grace := s.config.Global.Timeouts.ShutdownGrace.Duration
	if grace == 0 {
		grace = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	if s.httpSrv != nil {
		s.httpSrv.Shutdown(ctx)
	}
	if s.httpsSrv != nil {
		s.httpsSrv.Shutdown(ctx)
	}

	s.logger.Info("all connections drained")
}
