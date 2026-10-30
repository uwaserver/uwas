package server

import (
	"net"
	"testing"
)

// F57000: Start launches the registered apps before it binds any listener. A
// bind failure returned without stopping them; apps run in their own process
// group (Setpgid), so they outlived the failed instance, kept their ports and
// made the next start's apps fail with "address in use".
func TestStartBindFailureStopsAlreadyStartedApps(t *testing.T) {
	s, mgr := bootServerWithApp(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	s.config.Global.HTTPListen = ln.Addr().String()

	if err := s.Start(); err == nil {
		t.Fatal("Start succeeded on an occupied port; expected a bind failure")
	}
	if inst := mgr.Get("demo"); inst != nil && inst.Running {
		t.Fatalf("app demo still running after Start failed (pid %d): orphaned by the failed start", inst.PID)
	}
}
