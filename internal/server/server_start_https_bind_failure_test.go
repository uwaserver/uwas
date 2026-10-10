package server

// Regression guard: when the HTTP listener came up and the HTTPS bind then
// failed, joinOnErr cancelled ctx and waited on s.wg — but the HTTP serve
// goroutine is in s.wg and only returns once httpSrv is closed, which nothing
// did. Start parked in Wait forever with port 80 still serving, instead of
// returning the bind error the way an HTTP bind failure does.

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestStartHTTPSBindFailureReturnsAndReleasesHTTP(t *testing.T) {
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpAddr := httpLn.Addr().String()
	httpLn.Close()

	// Hold the HTTPS port for the whole test so that bind fails deterministically.
	httpsLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpsLn.Close()

	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: httpAddr, HTTPSListen: httpsLn.Addr().String(),
			ACME: config.ACMEConfig{Storage: filepath.Join(dir, "certs")},
		},
		Domains: []config.Domain{{Host: "bindfail.test", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "auto"}}},
	}
	s := New(cfg, logger.New("error", "text"))

	done := make(chan error, 1)
	go func() { done <- s.Start() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Start succeeded although the HTTPS port is occupied")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the HTTPS bind failed — parked in joinOnErr's wg.Wait with the HTTP listener still serving")
	}

	// The HTTP listener that did start must have been released.
	ln, err := net.Listen("tcp", httpAddr)
	if err != nil {
		t.Fatalf("HTTP port still held after Start returned: %v", err)
	}
	ln.Close()
}
