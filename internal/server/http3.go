package server

import (
	"fmt"
	"net/http"

	"github.com/quic-go/quic-go/http3"
)

// startHTTP3 starts an HTTP/3 (QUIC) listener on the same address as HTTPS.
// HTTP/3 uses UDP while HTTP/2 uses TCP — they can share the same port.
func (s *Server) startHTTP3() error {
	addr := s.config.Global.HTTPSListen
	tlsCfg := s.tlsMgr.TLSConfig()

	h3srv := &http3.Server{
		Addr:      addr,
		TLSConfig: http3.ConfigureTLSConfig(tlsCfg),
		Handler:   s.handler,
	}

	s.h3srv = h3srv

	s.logger.Info("listening", "address", addr, "protocol", "HTTP/3 (QUIC)")

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := h3srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("http/3 serve error", "error", err)
		}
	}()

	return nil
}

// altSvcHeader returns the Alt-Svc header value advertising HTTP/3 support.
// This header is added to HTTP/1.1 and HTTP/2 responses so browsers know
// they can upgrade to HTTP/3.
func (s *Server) altSvcHeader() string {
	if !s.config.Global.HTTP3Enabled || s.h3srv == nil {
		return ""
	}
	// Extract port from HTTPS listen address
	addr := s.config.Global.HTTPSListen
	port := "443"
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			port = addr[i+1:]
			break
		}
	}
	return fmt.Sprintf(`h3=":%s"; ma=86400`, port)
}
