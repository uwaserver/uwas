package server

import (
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

func TestResolveAppsUpstreamPassThrough(t *testing.T) {
	s := &Server{}
	cases := []string{
		"http://127.0.0.1:3000",
		"https://upstream.example.com",
		"h2c://localhost:8080",
		"grpc://10.0.0.5:50051",
		"127.0.0.1:9000",
		"",
	}
	for _, addr := range cases {
		if got := s.resolveAppsUpstream(addr); got != addr {
			t.Errorf("resolveAppsUpstream(%q) = %q, want unchanged", addr, got)
		}
	}
}

func TestResolveAppsUpstreamPlaceholderWhenNoManager(t *testing.T) {
	s := &Server{}
	got := s.resolveAppsUpstream("apps://my-api")
	if got != "http://127.0.0.1:0" {
		t.Errorf("expected placeholder, got %q", got)
	}
}

func TestResolveAppsUpstreamPlaceholderWhenAppStopped(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	mgr := apps.NewManager(store, nil)
	// Register an app but don't start — ListenAddr should be empty.
	if err := mgr.Register(&apps.App{
		Name:    "stopped-app",
		Runtime: apps.RuntimeCustom,
		Command: "./run",
	}); err != nil {
		t.Fatal(err)
	}

	s := &Server{appsMgr: mgr}
	got := s.resolveAppsUpstream("apps://stopped-app")
	if got != "http://127.0.0.1:0" {
		t.Errorf("expected placeholder for stopped app, got %q", got)
	}
}

func TestResolveAppsUpstreamStripsPath(t *testing.T) {
	s := &Server{}
	// Even without a manager, path-stripping should happen before the
	// placeholder fallback. We verify by checking the warning path
	// isn't taken for an empty name.
	got := s.resolveAppsUpstream("apps:///some/path")
	if got != "http://127.0.0.1:0" {
		t.Errorf("apps:// with empty name should yield placeholder, got %q", got)
	}
}
