package server

import (
	"net"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func newHTTP3TestServer(addr string) *Server {
	cfg := &config.Config{Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text",
		HTTPSListen: addr, HTTP3Enabled: true}}
	return New(cfg, logger.New("error", "text"))
}

// F35000: when the UDP port for HTTP/3 cannot be bound, the server kept
// advertising `Alt-Svc: h3=":443"` on every HTTP/1.1 and HTTP/2 response.
// Browsers then try QUIC against a port nobody serves, and fall back to TCP
// only after a timeout, on every new connection, for the 24 h `ma` lifetime.
func TestHTTP3AltSvcNotAdvertisedWhenUDPBindFails(t *testing.T) {
	busy, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind UDP: %v", err)
	}
	defer busy.Close()

	s := newHTTP3TestServer(busy.LocalAddr().String())
	t.Cleanup(func() { s.cancel() })
	startErr := s.startHTTP3()
	s.wg.Wait() // the serve goroutine, if any, has returned
	if s.h3srv != nil {
		defer s.h3srv.Close()
	}
	got := s.altSvcHeader()
	t.Logf("EXPECTED: no Alt-Svc after a failed UDP bind\nACTUAL: startHTTP3 err=%v Alt-Svc=%q", startErr, got)
	if got != "" {
		t.Fatalf("PROBLEM CONFIRMED: Alt-Svc %q advertised although the HTTP/3 listener failed to bind", got)
	}
}

// Control: a free UDP port still advertises HTTP/3.
func TestHTTP3AltSvcAdvertisedWhenUDPBindSucceeds(t *testing.T) {
	s := newHTTP3TestServer("127.0.0.1:0")
	t.Cleanup(func() { s.cancel() })
	if err := s.startHTTP3(); err != nil {
		t.Fatal(err)
	}
	defer func() { s.h3srv.Close(); s.wg.Wait() }()
	if got := s.altSvcHeader(); got == "" {
		t.Fatal("Alt-Svc must be advertised when the HTTP/3 listener is up")
	}
}

// F35001: startHTTP3 assigns s.h3srv after the TCP listeners already serve
// requests; altSvcHeader reads it on the request path. Run with -race.
func TestHTTP3StartRacesWithAltSvcRead(t *testing.T) {
	s := newHTTP3TestServer("127.0.0.1:0")
	t.Cleanup(func() { s.cancel() })
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			_ = s.altSvcHeader()
		}
	}()
	close(start)
	if err := s.startHTTP3(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	s.h3srv.Close()
	s.wg.Wait()
}
