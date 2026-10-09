package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A NUL byte in db_pass (JSON "\u0000") used to panic inside sqlString after
// the files were copied and the target database created, leaving a non-empty
// target that blocked every retry. Clone must reject it before any side effect.
func TestCloneRejectsNULPasswordBeforeSideEffects(t *testing.T) {
	restore, sawCreateUser := fakeMigrateExec(false)
	defer restore()
	origFiles, origChown := runCloneFiles, runCloneChown
	defer func() { runCloneFiles, runCloneChown = origFiles, origChown }()
	copied := false
	runCloneFiles = func(src, dst string, _ *strings.Builder) error {
		copied = true
		return os.WriteFile(filepath.Join(dst, "index.php"), []byte("<?php"), 0644)
	}
	runCloneChown = func(string) {}

	target := filepath.Join(t.TempDir(), "staging")
	req := CloneRequest{SourceDomain: "a.com", TargetDomain: "staging.a.com",
		SourceRoot: t.TempDir(), TargetRoot: target, SourceDB: "srcdb", DBUser: "appuser", DBPass: "x\x00y"}

	res := Clone(req)
	if res.Status != "error" || res.Error != "invalid database password" {
		t.Fatalf("Clone with NUL password = %+v, want status=error invalid database password", res)
	}
	if copied || *sawCreateUser {
		t.Fatalf("side effects before rejection: copied=%v createUser=%v", copied, *sawCreateUser)
	}

	req.DBPass = "good-pass"
	if res := Clone(req); res.Status != "done" {
		t.Fatalf("retry with a valid password on the same target = %+v, want done", res)
	}
}
