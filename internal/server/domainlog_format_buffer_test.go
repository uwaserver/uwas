package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// access_log.format and access_log.buffer_size were dead configuration: the
// writer always emitted an unbuffered CLF-like line and read neither field,
// while SPECIFICATION.md documents `format: json | clf | custom` and a buffer
// size. Only path and rotate ever reached this code.

func logFixture(t *testing.T, cfg config.AccessLogConfig) (*domainLogManager, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "access.log")
	cfg.Path = path
	m := newDomainLogManager()
	t.Cleanup(m.Close)
	return m, path
}

func writeOneLine(m *domainLogManager, cfg config.AccessLogConfig, path string) {
	cfg.Path = path
	m.Write("log.test", cfg, "GET", "/index.html?a=1", "203.0.113.7", "uwas-test",
		200, 1024, 5*time.Millisecond)
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log unreadable: %v", err)
	}
	return string(data)
}

func TestAccessLogJSONFormat(t *testing.T) {
	cfg := config.AccessLogConfig{Format: "json"}
	m, path := logFixture(t, cfg)
	writeOneLine(m, cfg, path)

	line := strings.TrimSpace(readLog(t, path))
	var got struct {
		Time       string `json:"time"`
		RemoteIP   string `json:"remote_ip"`
		Method     string `json:"method"`
		Path       string `json:"path"`
		Status     int    `json:"status"`
		Bytes      int    `json:"bytes"`
		DurationMS int64  `json:"duration_ms"`
		UserAgent  string `json:"user_agent"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("format=json did not produce JSON: %v\n  line: %s", err, line)
	}
	if got.RemoteIP != "203.0.113.7" || got.Method != "GET" || got.Path != "/index.html?a=1" {
		t.Errorf("fields are wrong: %+v", got)
	}
	if got.Status != 200 || got.Bytes != 1024 || got.DurationMS != 5 {
		t.Errorf("numeric fields are wrong: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.Time); err != nil {
		t.Errorf("time is not RFC3339: %q", got.Time)
	}
}

// The default must not change for anyone who never set format.
func TestAccessLogDefaultsToCLF(t *testing.T) {
	cfg := config.AccessLogConfig{}
	m, path := logFixture(t, cfg)
	writeOneLine(m, cfg, path)

	line := readLog(t, path)
	if !strings.HasPrefix(line, "203.0.113.7 - - [") {
		t.Errorf("the default format is not CLF: %q", line)
	}
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		t.Error("the default turned into JSON")
	}
}

func TestAccessLogCLFExplicit(t *testing.T) {
	cfg := config.AccessLogConfig{Format: "clf"}
	m, path := logFixture(t, cfg)
	writeOneLine(m, cfg, path)

	if line := readLog(t, path); !strings.Contains(line, `"GET /index.html?a=1"`) {
		t.Errorf("the clf line is not in the expected shape: %q", line)
	}
}

// "custom" is documented but there is no format-string field to carry a
// template; it must fall back rather than produce nothing.
func TestAccessLogCustomAndUnknownFallBackToCLF(t *testing.T) {
	for _, format := range []string{"custom", "nonsense"} {
		cfg := config.AccessLogConfig{Format: format}
		m, path := logFixture(t, cfg)
		writeOneLine(m, cfg, path)

		if line := readLog(t, path); !strings.HasPrefix(line, "203.0.113.7 - - [") {
			t.Errorf("format=%q did not fall back to clf: %q", format, line)
		}
	}
}

// buffer_size must actually hold the line back, and Close must flush it.
func TestAccessLogBufferHoldsThenFlushesOnClose(t *testing.T) {
	cfg := config.AccessLogConfig{BufferSize: 64 << 10}
	path := filepath.Join(t.TempDir(), "access.log")
	cfg.Path = path

	m := newDomainLogManager()
	writeOneLine(m, cfg, path)

	if data, _ := os.ReadFile(path); len(data) != 0 {
		t.Errorf("the line was written immediately with buffer_size set: %q", data)
	}

	m.Close()

	if line := readLog(t, path); !strings.Contains(line, "203.0.113.7") {
		t.Errorf("Close did not flush the buffer: %q", line)
	}
}

// Unbuffered stays the default: a buffered log loses its tail if the process
// dies, so the operator has to ask for it.
func TestAccessLogUnbufferedByDefault(t *testing.T) {
	cfg := config.AccessLogConfig{}
	m, path := logFixture(t, cfg)
	writeOneLine(m, cfg, path)

	if data, _ := os.ReadFile(path); len(data) == 0 {
		t.Error("the default behaved as buffered — the line is not in the file")
	}
}

// Rotation renames the file; anything still buffered would land in the new
// one. It must be flushed first.
func TestAccessLogBufferFlushedBeforeRotate(t *testing.T) {
	cfg := config.AccessLogConfig{
		BufferSize: 64 << 10,
		Rotate:     config.RotateConfig{MaxSize: 1}, // every line triggers a rotation
	}
	path := filepath.Join(t.TempDir(), "access.log")
	cfg.Path = path

	m := newDomainLogManager()
	writeOneLine(m, cfg, path)
	m.Close()

	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("directory unreadable: %v", err)
	}
	var toplam int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		toplam += info.Size()
	}
	if toplam == 0 {
		t.Error("no file holds any content after rotation — the buffer was lost")
	}
}

func TestAccessLogLineRendering(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	clf := accessLogLine("", now, "POST", "/x", "10.0.0.1", "UA", 201, 7, 3*time.Millisecond)
	if !strings.HasSuffix(clf, "\n") {
		t.Error("the clf line does not end with a newline")
	}
	if !strings.Contains(clf, `"POST /x" 201 7 3ms "UA"`) {
		t.Errorf("clf = %q", clf)
	}

	js := accessLogLine("JSON", now, "POST", "/x", "10.0.0.1", "UA", 201, 7, 3*time.Millisecond)
	if !strings.HasSuffix(js, "\n") {
		t.Error("the json line does not end with a newline")
	}
	if !json.Valid([]byte(strings.TrimSpace(js))) {
		t.Errorf("invalid json: %q", js)
	}
}

// A buffered log must reach disk on its own while traffic is light, or
// `tail -f` shows nothing until shutdown.
func TestAccessLogBufferFlushesOnTimer(t *testing.T) {
	cfg := config.AccessLogConfig{BufferSize: 64 << 10}
	path := filepath.Join(t.TempDir(), "access.log")
	cfg.Path = path

	m := newDomainLogManager()
	t.Cleanup(m.Close)
	m.StartCleanup()

	writeOneLine(m, cfg, path)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, _ := os.ReadFile(path); len(data) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the buffered line never reached disk on the timer")
}

func TestKnownAccessLogFormat(t *testing.T) {
	for _, ok := range []string{"", "clf", "CLF", "json", " json ", "custom"} {
		if !KnownAccessLogFormat(ok) {
			t.Errorf("%q was not recognised", ok)
		}
	}
	for _, kotu := range []string{"combined", "json_lines", "nonsense"} {
		if KnownAccessLogFormat(kotu) {
			t.Errorf("%q was recognised", kotu)
		}
	}
}
