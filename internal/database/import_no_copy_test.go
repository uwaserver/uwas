//go:build unix

package database

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// ImportDatabase used to wrap the payload in strings.NewReader(string(sqlData)),
// copying up to 256 MiB per client attempt (F2410). The client must receive the
// exact bytes without that copy.
func TestImportDatabaseStreamsWithoutCopy(t *testing.T) {
	dir := t.TempDir()
	sink := filepath.Join(dir, "stdin.sql")
	bin := filepath.Join(dir, "mariadb")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >"+sink+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldLook, oldCmd := execLookPathFn, execCommandFn
	execLookPathFn = func(string) (string, error) { return bin, nil }
	execCommandFn = exec.Command
	t.Cleanup(func() { execLookPathFn, execCommandFn = oldLook, oldCmd })

	const size = 32 << 20
	data := bytes.Repeat([]byte("INSERT INTO t VALUES (1);\n"), size/26)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := ImportDatabase("shop", data); err != nil {
		t.Fatalf("import: %v", err)
	}
	runtime.ReadMemStats(&after)
	if extra := after.TotalAlloc - before.TotalAlloc; extra > uint64(len(data))/4 {
		t.Errorf("import allocated %d extra bytes for a %d-byte dump; want a streamed reader (<= %d)", extra, len(data), len(data)/4)
	}
	got, err := os.ReadFile(sink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("client received %d bytes, want the %d-byte payload unchanged", len(got), len(data))
	}

	// Edge: an invalid database name is still refused before any client runs.
	if err := ImportDatabase("bad name;", data); err == nil {
		t.Error("invalid database name accepted")
	}
}
