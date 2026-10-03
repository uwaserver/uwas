package migrate

// Regression tests for the clone root-path contract (see Clone).
//
// The admin handler resolves ABSOLUTE roots (domain roots and
// filepath.Join(webRoot, ...)), so Clone must accept them; and because
// cloneFilesReal runs rsync --delete onto the target, an existing target root
// must be refused rather than silently overwritten.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCloneAcceptsAbsoluteRootsFromHandler pins that roots resolved by the
// admin handler (absolute paths under the web root) produce a successful
// clone. The old blanket rejection of absolute paths made every
// panel-initiated clone fail with "must be relative paths" before a byte was
// copied.
func TestCloneAcceptsAbsoluteRootsFromHandler(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "web", "a.com", "public_html")
	dst := filepath.Join(base, "web", "staging.b.com", "public_html")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.php"), []byte("<?php echo 1;"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Clone(CloneRequest{
		SourceDomain: "a.com",
		TargetDomain: "staging.b.com",
		SourceRoot:   src,
		TargetRoot:   dst,
	})
	if res.Status != "done" {
		t.Fatalf("clone with handler-resolved absolute roots failed: status=%q error=%q", res.Status, res.Error)
	}
	if _, err := os.Stat(filepath.Join(dst, "index.php")); err != nil {
		t.Fatalf("clone reported done but the target file is missing: %v", err)
	}
}

// TestCloneRefusesExistingTargetRoot pins the API-side enforcement of the
// panel's "target already exists" contract: rsync --delete onto an existing
// target would destroy that site's files and the deterministic staging DB
// name would clobber its tables, all reported as "done".
func TestCloneRefusesExistingTargetRoot(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "existing-target")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.php"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(dst, "precious.txt")
	if err := os.WriteFile(precious, []byte("existing staging content"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Clone(CloneRequest{
		SourceDomain: "a.com",
		TargetDomain: "b.com",
		SourceRoot:   src,
		TargetRoot:   dst,
	})
	if res.Status != "error" {
		t.Fatalf("clone onto an existing target returned status=%q — the existing site would be silently overwritten", res.Status)
	}
	if !strings.Contains(res.Error, "already exists") {
		t.Errorf("error = %q, want it to explain the existing-target refusal", res.Error)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatalf("existing target file was destroyed by the refused clone: %v", err)
	}
}

// TestCloneAllowsExistingEmptyTargetRoot pins the guard's boundary: an
// existing but EMPTY target directory (leftover from a failed clone) is
// harmless to fill — only a non-empty target is refused.
func TestCloneAllowsExistingEmptyTargetRoot(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "empty-target")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.php"), []byte("site"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Clone(CloneRequest{
		SourceDomain: "a.com",
		TargetDomain: "b.com",
		SourceRoot:   src,
		TargetRoot:   dst,
	})
	if res.Status != "done" {
		t.Fatalf("clone onto an existing empty target failed: status=%q error=%q", res.Status, res.Error)
	}
	if _, err := os.Stat(filepath.Join(dst, "index.php")); err != nil {
		t.Fatalf("cloned file missing: %v", err)
	}
}

// TestCloneFreshTargetSucceeds is the control: a clone onto a target that
// does not exist keeps succeeding.
func TestCloneFreshTargetSucceeds(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "fresh-target")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.php"), []byte("site"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Clone(CloneRequest{
		SourceDomain: "a.com",
		TargetDomain: "staging.a.com",
		SourceRoot:   src,
		TargetRoot:   dst,
	})
	if res.Status != "done" {
		t.Fatalf("fresh-target clone failed: status=%q error=%q", res.Status, res.Error)
	}
	if _, err := os.Stat(filepath.Join(dst, "index.php")); err != nil {
		t.Fatalf("cloned file missing: %v", err)
	}
}
