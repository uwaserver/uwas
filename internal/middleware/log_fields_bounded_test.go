package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/uwaserver/uwas/internal/logger"
)

// F14800: the listener accepts headers up to max_header_bytes (1 MiB by
// default), and the access log wrote User-Agent, Referer, Host and the query
// verbatim, so one request produced a log line that large.
// F14801: the hotlink-block warning logged the Referer unredacted, though the
// access log redacts sensitive query parameters in the same header.

// captureLogOutput runs fn with a text logger writing to a pipe that replaces
// os.Stdout (logger.New binds its writer at construction) and returns what was
// written. Tests in this package do not run in parallel, so the swap is safe.
func captureLogOutput(t *testing.T, fn func(log *logger.Logger)) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	log := logger.New("info", "text")
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn(log)
	log.Flush(time.Second) // logger writes asynchronously
	os.Stdout = orig
	_ = w.Close()
	return <-done
}

func accessLine(t *testing.T, mutate func(r *http.Request)) string {
	t.Helper()
	return captureLogOutput(t, func(log *logger.Logger) {
		h := AccessLog(log, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.RemoteAddr = "203.0.113.1:5000"
		req.Host = "example.com"
		mutate(req)
		h.ServeHTTP(httptest.NewRecorder(), req)
	})
}

func TestAccessLogBoundsRequestControlledFields(t *testing.T) {
	const bound = 8 << 10
	big := strings.Repeat("A", 500<<10)

	line := accessLine(t, func(r *http.Request) {
		r.Header.Set("User-Agent", big)
		r.Header.Set("Referer", "https://r.example/?"+big)
		r.Header.Set("Traceparent", big)
		r.Host = strings.Repeat("h", 60<<10) + ".example"
		r.URL.RawQuery = "q=" + big
	})
	if len(line) > bound {
		t.Fatalf("one request wrote a %d byte log line, want <= %d", len(line), bound)
	}
	if !strings.Contains(line, "truncated") {
		t.Errorf("a clipped value must be marked as clipped: %q", line[:200])
	}

	// Control: ordinary values are logged exactly as before.
	normal := accessLine(t, func(r *http.Request) {
		r.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")
		r.Header.Set("Referer", "https://ok.example/page")
	})
	for _, want := range []string{"Mozilla/5.0 (X11; Linux x86_64)", "https://ok.example/page", "example.com"} {
		if !strings.Contains(normal, want) {
			t.Errorf("ordinary value %q missing from %q", want, normal)
		}
	}
	if strings.Contains(normal, "truncated") {
		t.Errorf("ordinary values must not be clipped: %q", normal)
	}
}

func TestClipLogField(t *testing.T) {
	if got := clipLogField("abc", 3); got != "abc" {
		t.Errorf("value at the limit changed: %q", got)
	}
	if got := clipLogField("", 3); got != "" {
		t.Errorf("empty value changed: %q", got)
	}
	got := clipLogField("abcdef", 3)
	if !strings.HasPrefix(got, "abc") || !strings.Contains(got, "truncated 3 bytes") {
		t.Errorf("clip of abcdef = %q", got)
	}
	// "é" is two bytes; a cut in the middle of it must not leave half a rune.
	for max := 1; max < 8; max++ {
		out := clipLogField("éééééé", max)
		if !utf8.ValidString(out) {
			t.Errorf("max %d: clipped value is not valid UTF-8: %q", max, out)
		}
	}
}

func TestHotlinkWarningRedactsRefererSecrets(t *testing.T) {
	const secret = "S3CRET-TOKEN-VALUE"
	out := captureLogOutput(t, func(log *logger.Logger) {
		guard := HotlinkGuard(log, []string{"example.com"}, nil)
		req := httptest.NewRequest(http.MethodGet, "/images/photo.jpg", nil)
		req.Header.Set("Referer", "https://evil.example/reset?page=2&token="+secret)
		req.Host = "example.com"
		if guard(httptest.NewRecorder(), req) {
			t.Fatal("guard allowed the foreign referer")
		}
	})
	if !strings.Contains(out, "hotlink blocked") {
		t.Fatalf("no hotlink warning was written: %q", out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("hotlink warning leaked the Referer's token: %q", out)
	}
	if !strings.Contains(out, "page=2") {
		t.Errorf("non-sensitive Referer parameters should survive: %q", out)
	}

	// The warning is bounded like the access log line.
	big := captureLogOutput(t, func(log *logger.Logger) {
		guard := HotlinkGuard(log, []string{"example.com"}, nil)
		req := httptest.NewRequest(http.MethodGet, "/images/photo.jpg", nil)
		req.Header.Set("Referer", "https://evil.example/?x="+strings.Repeat("A", 500<<10))
		req.Host = "example.com"
		guard(httptest.NewRecorder(), req)
	})
	if len(big) > 8<<10 {
		t.Errorf("hotlink warning was %d bytes for one request", len(big))
	}
}
