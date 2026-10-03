package admin

// Regression tests for the clone root-path resolution contract (see
// resolveClonePaths): caller-supplied relative roots resolve against the web
// root (not the process working directory), the target must stay under the
// web root, and an absolute source outside it (an app workdir) is allowed —
// the source is only read.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/migrate"
)

func newCloneTestServer(webRoot string) *Server {
	s := &Server{config: &config.Config{}}
	s.config.Global.WebRoot = webRoot
	return s
}

// TestResolveClonePathsResolvesRelativeRootsAgainstWebRoot pins that
// caller-supplied relative roots land under the web root instead of the UWAS
// process working directory.
func TestResolveClonePathsResolvesRelativeRootsAgainstWebRoot(t *testing.T) {
	webRoot := t.TempDir()
	s := newCloneTestServer(webRoot)

	req := &migrate.CloneRequest{
		SourceDomain: "a.com",
		SourceRoot:   "a.com/public_html",
		TargetDomain: "staging.b.com",
		TargetRoot:   "staging.b.com/public_html",
	}
	if err := s.resolveClonePaths(req); err != nil {
		t.Fatalf("resolveClonePaths: %v", err)
	}
	if want := filepath.Join(webRoot, "a.com", "public_html"); req.SourceRoot != want {
		t.Errorf("source root = %q, want %q", req.SourceRoot, want)
	}
	if want := filepath.Join(webRoot, "staging.b.com", "public_html"); req.TargetRoot != want {
		t.Errorf("target root = %q, want %q", req.TargetRoot, want)
	}
}

// TestResolveClonePathsRejectsTargetOutsideWebRoot pins the containment guard
// on the destructive destination.
func TestResolveClonePathsRejectsTargetOutsideWebRoot(t *testing.T) {
	webRoot := t.TempDir()
	s := newCloneTestServer(webRoot)

	for name, target := range map[string]string{
		"traversal":     "../outside",
		"absolute-sibl": filepath.Join(filepath.Dir(webRoot), "elsewhere"),
	} {
		req := &migrate.CloneRequest{
			SourceDomain: "a.com",
			SourceRoot:   "a.com/public_html",
			TargetDomain: "b.com",
			TargetRoot:   target,
		}
		err := s.resolveClonePaths(req)
		if err == nil {
			t.Errorf("%s target %q: expected rejection, got nil", name, target)
			continue
		}
		if !strings.Contains(err.Error(), "escapes the web root") {
			t.Errorf("%s target: error = %v, want the escapes-the-web-root message", name, err)
		}
	}
}

// TestResolveClonePathsAllowsAppWorkdirSourceOutsideWebRoot pins the design
// decision that the source is only read: app workdirs live outside the web
// root (/var/lib/uwas/apps/<name>) and remain valid clone sources.
func TestResolveClonePathsAllowsAppWorkdirSourceOutsideWebRoot(t *testing.T) {
	webRoot := t.TempDir()
	s := newCloneTestServer(webRoot)

	req := &migrate.CloneRequest{
		SourceDomain: "myapp",
		SourceRoot:   filepath.Join(t.TempDir(), "app-workdir"),
		TargetDomain: "staging.myapp",
	}
	if err := s.resolveClonePaths(req); err != nil {
		t.Fatalf("app workdir source rejected: %v", err)
	}
	if want := filepath.Join(webRoot, "staging.myapp", "public_html"); req.TargetRoot != want {
		t.Errorf("target root = %q, want %q", req.TargetRoot, want)
	}
}
