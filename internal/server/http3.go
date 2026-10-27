package server

import (
	"fmt"
	"net"
	"net/http"

	"github.com/quic-go/quic-go/http3"
)

// startHTTP3 starts an HTTP/3 (QUIC) listener on the same address as HTTPS.
// HTTP/3 uses UDP while HTTP/2 uses TCP — they can share the same port.
func (s *Server) startHTTP3() error {
	addr := s.config.Global.HTTPSListen
	tlsCfg := s.tlsMgr.TLSConfig()

	// Bind the UDP port here, not inside the serve goroutine: a bind failure
	// must reach the caller and keep Alt-Svc from advertising a listener that
	// does not exist (F35000).
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}

	h3srv := &http3.Server{
		Addr:      addr,
		TLSConfig: http3.ConfigureTLSConfig(tlsCfg),
		// Same per-request read and write limits as the TCP HTTPS listener
		// (F15000), on top of the header and idle limits below (F3100).
		Handler: readDeadline(
			writeProgress(s.configuredHostsOnly(s.handler), s.config.Global.Timeouts.Write.Duration),
			s.config.Global.Timeouts.Read.Duration),
		MaxHeaderBytes: s.config.Global.Timeouts.MaxHeaderBytes,
		IdleTimeout:    s.config.Global.Timeouts.Idle.Duration,
	}

	// Request goroutines already read h3srv through altSvcHeader (F35001).
	s.cfgMu().Lock()
	s.h3srv = h3srv
	s.cfgMu().Unlock()

	s.logger.Info("listening", "address", addr, "protocol", "HTTP/3 (QUIC)")

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// Serve does not close the connection it is given.
		defer conn.Close()
		if err := h3srv.Serve(conn); err != nil && err != http.ErrServerClosed {
			s.logger.Error("http/3 serve error", "error", err)
		}
	}()

	return nil
}

// altSvcHeader returns the Alt-Svc header value advertising HTTP/3 support.
// This header is added to HTTP/1.1 and HTTP/2 responses so browsers know
// they can upgrade to HTTP/3.
func (s *Server) altSvcHeader() string {
	// Read config under the lock: this runs on the request hot path and reload
	// rewrites *s.config under configMu (data race otherwise).
	s.cfgMu().RLock()
	enabled := s.config.Global.HTTP3Enabled
	addr := s.config.Global.HTTPSListen
	h3 := s.h3srv
	s.cfgMu().RUnlock()

	if !enabled || h3 == nil {
		return ""
	}
	// Extract port from HTTPS listen address
	port := "443"
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			port = addr[i+1:]
			break
		}
	}
	return fmt.Sprintf(`h3=":%s"; ma=86400`, port)
}
