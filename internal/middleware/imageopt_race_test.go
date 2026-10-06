package middleware

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// converterHarness drives convertImage against a scripted /bin/sh converter.
// The converter writes the output path the production code passes (read from
// the hooked exec args, like a real cwebp/avifenc would honor -o), so the
// harness stays faithful whether production writes dst directly or a temp
// file it renames.
//
// park=true: the converter writes a PARTIAL output, touches the started flag,
// and waits for the release flag before writing the full payload — modeling a
// conversion in flight. park=false: it writes the full payload immediately.
type converterHarness struct {
	t           *testing.T
	src         string
	dst         string
	full        []byte
	startedFlag string
	releaseFlag string

	origLookPath func(string) (string, error)
	origExec     func(string, ...string) *exec.Cmd
}

func newConverterHarness(t *testing.T, park bool) *converterHarness {
	t.Helper()
	dir := t.TempDir()
	h := &converterHarness{
		t:           t,
		src:         filepath.Join(dir, "photo.jpg"),
		dst:         filepath.Join(dir, "photo.jpg.webp"),
		full:        []byte("COMPLETE-WEBP-BYTES"),
		startedFlag: filepath.Join(dir, "started.flag"),
		releaseFlag: filepath.Join(dir, "release.flag"),
	}
	if err := os.WriteFile(h.src, []byte("jpeg-bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	h.origLookPath, h.origExec = convertLookPathFn, convertExecFn
	convertLookPathFn = func(string) (string, error) { return "/bin/sh", nil }
	convertExecFn = func(name string, args ...string) *exec.Cmd {
		out := args[len(args)-1] // the production code passes the output path last
		var script string
		if park {
			// partial FIRST, started-flag SECOND: once the flag exists, the
			// partial output at out is guaranteed to exist too.
			script = "#!/bin/sh\n" +
				"printf 'PARTIAL-WEBP' > " + out + "\n" +
				"touch " + h.startedFlag + "\n" +
				"while [ ! -f " + h.releaseFlag + " ]; do sleep 0.05; done\n" +
				"printf 'COMPLETE-WEBP-BYTES' > " + out + "\n"
		} else {
			script = "#!/bin/sh\nprintf 'COMPLETE-WEBP-BYTES' > " + out + "\n"
		}
		return exec.Command("/bin/sh", "-c", script)
	}
	t.Cleanup(func() {
		convertLookPathFn, convertExecFn = h.origLookPath, h.origExec
		// Always release a parked converter so the test cannot hang.
		_ = os.WriteFile(h.releaseFlag, nil, 0644)
	})
	return h
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// TestConvertImageConcurrentWaitsForInFlightWrite pins the TOCTOU contract:
// a request arriving while a conversion is in flight must not be told the
// conversion succeeded until dst holds the COMPLETE converted bytes. The
// pre-lock Stat(dst) fast path in convertImageReal returned success on the
// converter's partially written destination, so the caller served a corrupt
// image. Post-fix (convert to temp + atomic rename) a concurrent caller
// blocks until the rename publishes the complete file.
func TestConvertImageConcurrentWaitsForInFlightWrite(t *testing.T) {
	h := newConverterHarness(t, true)

	var wg sync.WaitGroup
	g2Saw := make(chan string, 1)

	// A: the in-flight conversion. The script writes PARTIAL before touching
	// the started flag, so once waitForFile returns, h.dst deterministically
	// holds partial bytes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = convertImage(h.src, h.dst, "webp")
	}()
	waitForFile(t, h.startedFlag)

	// B: the concurrent request. Pre-fix it early-exits with success while
	// dst is partial; post-fix it blocks on convertMu until A finishes and
	// then re-converts over the published file.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ok := convertImage(h.src, h.dst, "webp")
		content, readErr := os.ReadFile(h.dst)
		if !ok {
			g2Saw <- "<failed>"
			return
		}
		if readErr != nil {
			g2Saw <- "<unreadable>"
			return
		}
		g2Saw <- string(content)
	}()

	// Settle window: gives B the chance to take the buggy early exit. Post-fix
	// B spends this window blocked on convertMu. Bounded; not a flake source.
	time.Sleep(300 * time.Millisecond)

	if err := os.WriteFile(h.releaseFlag, nil, 0644); err != nil {
		t.Fatalf("release in-flight conversion: %v", err)
	}
	wg.Wait()

	if seen := <-g2Saw; seen != string(h.full) {
		t.Fatalf("concurrent caller got success while dst held %q; a request would serve a corrupt image", seen)
	}
}

// Control: a single sequential conversion completes with the full payload.
func TestConvertImageSequentialCompletes(t *testing.T) {
	h := newConverterHarness(t, false)

	if !convertImage(h.src, h.dst, "webp") {
		t.Fatal("conversion reported failure")
	}
	got, err := os.ReadFile(h.dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(h.full) {
		t.Fatalf("dst holds %d bytes, want the full payload", len(got))
	}
}
