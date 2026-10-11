//go:build unix

package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// The restore path used to wrap the dump in strings.NewReader(string(data)),
// copying a dump of up to 2 GiB a second time before piping it to mysql
// (F2410). The client must receive the exact bytes without that copy.
func TestImportDatabaseDumpRealStreamsWithoutCopy(t *testing.T) {
	dir := t.TempDir()
	sink := filepath.Join(dir, "stdin.sql")
	script := "#!/bin/sh\ncat >" + sink + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mysql"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := logger.New("error", "text")

	const size = 32 << 20
	data := bytes.Repeat([]byte("INSERT INTO t VALUES (1);\n"), size/26)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := importDatabaseDumpReal(data, log); err != nil {
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
		t.Errorf("mysql received %d bytes, want the %d-byte dump unchanged", len(got), len(data))
	}

	// Edge: an empty dump is still delivered (and succeeds) without error.
	if err := importDatabaseDumpReal(nil, log); err != nil {
		t.Errorf("empty dump: %v", err)
	}
}
