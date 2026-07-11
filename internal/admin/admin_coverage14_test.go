package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// ============================================================================
// atomicWriteFile (61.5% -> target >95%)
// ============================================================================

func TestAtomicWriteFileSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")
	data := []byte("hello world")

	if err := atomicWriteFile(path, data, 0600); err != nil {
		t.Fatalf("atomicWriteFile failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content = %q, want %q", string(got), string(data))
	}
}

func TestAtomicWriteFileCreateTempFails(t *testing.T) {
	err := atomicWriteFile("/nonexistent/path/file", []byte("x"), 0600)
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
	if !strings.Contains(err.Error(), "create temp") {
		t.Errorf("error = %q, want 'create temp'", err.Error())
	}
}

func TestAtomicWriteFileLargeData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "largefile")
	data := make([]byte, 1<<20) // 1MB
	rand.Read(data)

	if err := atomicWriteFile(path, data, 0600); err != nil {
		t.Fatalf("atomicWriteFile(large) failed: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("content mismatch")
	}
}

func TestAtomicWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "collide")

	if err := atomicWriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := atomicWriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatalf("second write (overwrite) failed: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second" {
		t.Fatalf("content = %q, want 'second'", string(got))
	}
}

func TestAtomicWriteFileTempCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cleanup")

	if err := atomicWriteFile(path, []byte("cleanup test"), 0600); err != nil {
		t.Fatalf("atomicWriteFile failed: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("stale temp file found: %s", e.Name())
		}
	}
}

// ============================================================================
// readAuditLines (85.7% -> target ~100%)
// ============================================================================

func TestReadAuditLinesMissingFile(t *testing.T) {
	var tail []AuditEntry
	err := readAuditLines("/nonexistent/audit.log", &tail)
	if err != nil {
		t.Fatalf("readAuditLines on missing file: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("tail length = %d, want 0", len(tail))
	}
}

func TestReadAuditLinesEmptyLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(path, []byte("\n\n\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var tail []AuditEntry
	if err := readAuditLines(path, &tail); err != nil {
		t.Fatalf("readAuditLines failed: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("tail length = %d, want 0", len(tail))
	}
}

func TestReadAuditLinesMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	content := "not json\n{\"valid\": true}\nmore garbage\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	var tail []AuditEntry
	if err := readAuditLines(path, &tail); err != nil {
		t.Fatalf("readAuditLines failed: %v", err)
	}
	_ = tail
}

func TestReadAuditLinesValidEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	entry := AuditEntry{Action: "test.action", Success: true, Time: time.Now().UTC()}
	data, _ := json.Marshal(entry)
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	var tail []AuditEntry
	if err := readAuditLines(path, &tail); err != nil {
		t.Fatalf("readAuditLines failed: %v", err)
	}
	if len(tail) != 1 {
		t.Fatalf("tail length = %d, want 1", len(tail))
	}
	if tail[0].Action != "test.action" {
		t.Errorf("action = %q, want 'test.action'", tail[0].Action)
	}
}

func TestReadAuditLinesFullBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	var data []byte
	for i := 0; i < maxAuditEntries+10; i++ {
		entry := AuditEntry{Action: fmt.Sprintf("action.%d", i), Success: true, Time: time.Now().UTC()}
		line, _ := json.Marshal(entry)
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	var tail []AuditEntry
	if err := readAuditLines(path, &tail); err != nil {
		t.Fatalf("readAuditLines failed: %v", err)
	}
	if len(tail) > maxAuditEntries {
		t.Errorf("tail length = %d, want <= %d", len(tail), maxAuditEntries)
	}
	if len(tail) == maxAuditEntries {
		lastAction := fmt.Sprintf("action.%d", maxAuditEntries+9)
		if tail[len(tail)-1].Action != lastAction {
			t.Errorf("last action = %q, want %q", tail[len(tail)-1].Action, lastAction)
		}
	}
}

// ============================================================================
// appendAuditLine (75.0% -> target >90%)
// ============================================================================

func TestAppendAuditLineNoConfigPath(t *testing.T) {
	s := testServer()
	s.configPath = ""
	s.appendAuditLine(AuditEntry{Action: "test", Success: true})
}

func TestAppendAuditLineCreatesFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	s := testServer()
	s.configPath = cfgPath
	s.appendAuditLine(AuditEntry{Action: "test.create", Success: true, Time: time.Now().UTC()})

	auditPath := filepath.Join(dir, "audit.log")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}
	if !strings.Contains(string(data), "test.create") {
		t.Errorf("audit.log missing 'test.create': %s", string(data))
	}
}

func TestAppendAuditLineMultipleEntries(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	s := testServer()
	s.configPath = cfgPath

	for i := 0; i < 5; i++ {
		s.appendAuditLine(AuditEntry{Action: fmt.Sprintf("test.%d", i), Success: true, Time: time.Now().UTC()})
	}

	auditPath := filepath.Join(dir, "audit.log")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Errorf("lines = %d, want 5", len(lines))
	}
}

// ============================================================================
// rotateAuditLog (77.8% -> target >90%)
// ============================================================================

func TestRotateAuditLogBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	os.WriteFile(path, []byte("initial content\n"), 0600)

	s := testServer()
	s.rotateAuditLog(path)

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated file .1 not found: %v", err)
	}
}

func TestRotateAuditLogWithExistingRotations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	for i := 1; i <= 3; i++ {
		os.WriteFile(fmt.Sprintf("%s.%d", path, i), []byte(fmt.Sprintf("rotated %d\n", i)), 0600)
	}
	os.WriteFile(path, []byte("current\n"), 0600)

	s := testServer()
	s.rotateAuditLog(path)

	if _, err := os.Stat(path + ".3"); err != nil {
		t.Errorf(".3 missing after rotate: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Errorf(".2 missing after rotate: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf(".1 missing after rotate: %v", err)
	}
}

func TestRotateAuditLogNoExistingFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	s := testServer()
	s.rotateAuditLog(path)
}

// ============================================================================
// loadAuditLog (86.7% -> target >95%)
// ============================================================================

func TestLoadAuditLogWithRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	auditPath := filepath.Join(dir, "audit.log")
	for i := 3; i >= 1; i-- {
		entry := AuditEntry{Action: fmt.Sprintf("rotated.%d", i), Success: true, Time: time.Now().UTC()}
		line, _ := json.Marshal(entry)
		os.WriteFile(fmt.Sprintf("%s.%d", auditPath, i), append(line, '\n'), 0600)
	}
	entry := AuditEntry{Action: "current", Success: true, Time: time.Now().UTC()}
	line, _ := json.Marshal(entry)
	os.WriteFile(auditPath, append(line, '\n'), 0600)

	s := testServer()
	s.configPath = cfgPath
	if err := s.loadAuditLog(); err != nil {
		t.Fatalf("loadAuditLog failed: %v", err)
	}
	if s.auditBuf == nil {
		t.Fatal("auditBuf is nil after load")
	}
	snap := s.auditBuf.Snapshot()
	if len(snap) == 0 {
		t.Error("expected at least one audit entry, got 0")
	}
}

// ============================================================================
// cloudflare_state.go functions
// ============================================================================

func TestCloudflareStateFileEmptyConfig(t *testing.T) {
	s := testServer()
	s.configPath = ""
	if path := s.cloudflareStateFile(); path != "" {
		t.Errorf("cloudflareStateFile = %q, want empty", path)
	}
}

func TestCloudflareStateFileWithConfigPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	s := testServer()
	s.configPath = cfgPath
	path := s.cloudflareStateFile()
	if path == "" {
		t.Fatal("expected non-empty cloudflareStateFile path")
	}
	if !strings.HasSuffix(path, "cloudflare.json") {
		t.Errorf("path = %q, want ending in 'cloudflare.json'", path)
	}
}

func TestLoadCloudflareStateEmptyConfig(t *testing.T) {
	s := testServer()
	s.configPath = ""
	if err := s.loadCloudflareState(); err != nil {
		t.Fatalf("loadCloudflareState with empty config: %v", err)
	}
}

func TestLoadCloudflareStateMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	s := testServer()
	s.configPath = cfgPath
	if err := s.loadCloudflareState(); err != nil {
		t.Fatalf("loadCloudflareState missing file: %v", err)
	}
}

func TestLoadCloudflareStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)
	statePath := filepath.Join(dir, "cloudflare.json")
	os.WriteFile(statePath, []byte("not json"), 0600)

	s := testServer()
	s.configPath = cfgPath
	if err := s.loadCloudflareState(); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadCloudflareStateMigration(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	oldState := cloudflareState{
		Tunnels: []cloudflareTunnel{
			{Domain: "old.example.com", Name: "old-tunnel"},
		},
		Connected: true,
		Email:     "test@example.com",
		Token:     "test-token",
	}
	data, _ := json.Marshal(oldState)
	statePath := filepath.Join(dir, "cloudflare.json")
	os.WriteFile(statePath, data, 0600)

	s := testServer()
	s.configPath = cfgPath
	if err := s.loadCloudflareState(); err != nil {
		t.Fatalf("loadCloudflareState migration: %v", err)
	}

	cloudflareMu.Lock()
	cfg := cloudflareConfig
	cloudflareMu.Unlock()

	if cfg == nil {
		t.Fatal("cloudflareConfig is nil after load")
	}
	if cfg.SchemaVersion != cloudflareStateSchemaCurrent {
		t.Errorf("SchemaVersion = %d, want %d", cfg.SchemaVersion, cloudflareStateSchemaCurrent)
	}
	if len(cfg.Tunnels) != 1 {
		t.Fatalf("tunnels = %d, want 1", len(cfg.Tunnels))
	}
	if cfg.Tunnels[0].Hostname != "old.example.com" {
		t.Errorf("Hostname = %q, want 'old.example.com'", cfg.Tunnels[0].Hostname)
	}
	if cfg.Tunnels[0].Domain != "" {
		t.Errorf("Domain should be cleared after migration, got %q", cfg.Tunnels[0].Domain)
	}
}

func TestLoadCloudflareStateWithoutMigration(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	v2State := cloudflareState{
		SchemaVersion: cloudflareStateSchemaCurrent,
		Tunnels: []cloudflareTunnel{
			{Hostname: "new.example.com", Name: "new-tunnel", LocalTarget: "http://localhost:8080"},
		},
		Connected: true,
		Email:     "test@example.com",
		Token:     "test-token",
	}
	data, _ := json.Marshal(v2State)
	statePath := filepath.Join(dir, "cloudflare.json")
	os.WriteFile(statePath, data, 0600)

	s := testServer()
	s.configPath = cfgPath
	if err := s.loadCloudflareState(); err != nil {
		t.Fatalf("loadCloudflareState (no migration): %v", err)
	}

	cloudflareMu.Lock()
	cfg := cloudflareConfig
	cloudflareMu.Unlock()

	if cfg == nil {
		t.Fatal("cloudflareConfig is nil after load")
	}
	if cfg.SchemaVersion != cloudflareStateSchemaCurrent {
		t.Errorf("SchemaVersion = %d, want %d", cfg.SchemaVersion, cloudflareStateSchemaCurrent)
	}
}

func TestSaveCloudflareStateLockedEmptyConfig(t *testing.T) {
	s := testServer()
	s.configPath = ""
	if err := s.saveCloudflareStateLocked(); err != nil {
		t.Fatalf("saveCloudflareStateLocked with empty config: %v", err)
	}
}

func TestSaveCloudflareStateLockedStampsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	s := testServer()
	s.configPath = cfgPath

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		SchemaVersion: 0,
		Token:         "test-token",
		Email:         "test@example.com",
		Connected:     true,
	}
	err := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()

	if err != nil {
		t.Fatalf("saveCloudflareStateLocked: %v", err)
	}

	statePath := filepath.Join(dir, "cloudflare.json")
	data, _ := os.ReadFile(statePath)
	var loaded cloudflareState
	json.Unmarshal(data, &loaded)
	if loaded.SchemaVersion != cloudflareStateSchemaCurrent {
		t.Errorf("SchemaVersion = %d, want %d", loaded.SchemaVersion, cloudflareStateSchemaCurrent)
	}
}

func TestSaveCloudflareStateLockedWithConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	s := testServer()
	s.configPath = cfgPath

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token:     "test-token",
		Email:     "test@example.com",
		Connected: true,
		Tunnels:   []cloudflareTunnel{},
	}
	err := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()

	if err != nil {
		t.Fatalf("saveCloudflareStateLocked: %v", err)
	}

	statePath := filepath.Join(dir, "cloudflare.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("cloudflare.json not created: %v", err)
	}
}

// ============================================================================
// isExpensiveGET (80% -> 100%)
// ============================================================================

// ============================================================================
// SetConfigPath (66.7% -> 100%)
// ============================================================================

func TestSetConfigPathSetsPath(t *testing.T) {
	s := testServer()
	s.configPath = ""

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	s.SetConfigPath(cfgPath)
	if s.configPath != cfgPath {
		t.Errorf("configPath = %q, want %q", s.configPath, cfgPath)
	}
}

func TestSetConfigPathWithCloudflareState(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	statePath := filepath.Join(dir, "cloudflare.json")
	cs := cloudflareState{
		SchemaVersion: cloudflareStateSchemaCurrent,
		Token:         "test",
		Email:         "test@test.com",
		Connected:     true,
	}
	data, _ := json.Marshal(cs)
	os.WriteFile(statePath, data, 0600)

	s := testServer()
	s.configPath = ""

	s.SetConfigPath(cfgPath)

	cloudflareMu.Lock()
	cfg := cloudflareConfig
	cloudflareMu.Unlock()

	if cfg == nil {
		t.Fatal("cloudflareConfig should be loaded after SetConfigPath")
	}
	if !cfg.Connected {
		t.Error("expected connected=true")
	}
}

// ============================================================================
// handleSSELogs (50% -> target >75%)
// ============================================================================

func TestHandleSSELogsBasic(t *testing.T) {
	s := testServer()

	s.RecordLog(LogEntry{Host: "test.com", Method: "GET", Path: "/", Status: 200, Duration: "1ms"})
	s.RecordLog(LogEntry{Host: "test.com", Method: "POST", Path: "/api", Status: 201, Duration: "5ms"})

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/sse/logs", nil).WithContext(ctx)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 && rec.Code != 401 {
		t.Errorf("unexpected status: %d", rec.Code)
	}
}

func TestHandleSSELogsWithDomainFilter(t *testing.T) {
	s := testServer()

	s.RecordLog(LogEntry{Host: "filtered.com", Method: "GET", Path: "/", Status: 200, Duration: "1ms"})

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/sse/logs?domain=filtered.com", nil).WithContext(ctx)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 && rec.Code != 401 {
		t.Errorf("unexpected status: %d", rec.Code)
	}
}

// ============================================================================
// handleStatsDomains (66.7% -> 100%)
// ============================================================================

// ============================================================================
// handleAlerts (63.6% -> target >90%)
// ============================================================================

func TestHandleAlertsWithHostFiltering(t *testing.T) {
	s := testServer()
	s.alerter = alerting.New(true, "", logger.New("error", "text"))

	s.alerter.Alert(alerting.Alert{
		Level:   "warning",
		Message: "test alert with host",
		Host:    "example.com",
	})

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/alerts", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// ============================================================================
// handleLogs (73.3% -> target >95%)
// ============================================================================

func TestHandleLogsWithData(t *testing.T) {
	s := testServer()

	s.RecordLog(LogEntry{Host: "test.com", Method: "GET", Path: "/", Status: 200, Duration: "1ms"})
	s.RecordLog(LogEntry{Host: "test.com", Method: "POST", Path: "/api", Status: 201, Duration: "5ms"})

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var logs []any
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("log count = %d, want 2", len(logs))
	}
}

func TestHandleLogsTruncatesTo100(t *testing.T) {
	s := testServer()

	for i := 0; i < 150; i++ {
		s.RecordLog(LogEntry{Host: "test.com", Method: "GET", Path: "/", Status: 200, Duration: "1ms"})
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var logs []any
	json.Unmarshal(rec.Body.Bytes(), &logs)
	if len(logs) != 100 {
		t.Errorf("log count = %d, want 100", len(logs))
	}
}

// ============================================================================
// handleFeatures (70.7% -> target >90%)
// ============================================================================

func TestHandleFeaturesWithManagers(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/features", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["backups"] == nil {
		t.Error("backups feature missing from response")
	}
}

// ============================================================================
// handleMonitor (77.8% -> target >90%)
// ============================================================================

// ============================================================================
// handleTaskList / handleTaskGet non-admin (88.9% -> 100%)
// ============================================================================

func TestHandleTaskListNonAdmin(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "test-key"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	s.authMiddleware(requireJSONMiddleware(s.mux)).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleTaskGetNonAdmin(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "test-key"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/some-id", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	s.authMiddleware(requireJSONMiddleware(s.mux)).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// ============================================================================
// isLoopbackListenAddr
// ============================================================================

// ============================================================================
// handleReload without reloadFn
// ============================================================================

func TestHandleReloadNoReloadFn(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/reload", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

// ============================================================================
// handleCachePurge without cache
// ============================================================================

// ============================================================================
// handleStats — already 100%
// ============================================================================

func TestHandleStats(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stats", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["requests_total"] == nil {
		t.Error("missing requests_total")
	}
	if body["latency_p50_ms"] == nil {
		t.Error("missing latency_p50_ms")
	}
}

// ============================================================================
// notifyDomainChange (75% -> 100%)
// ============================================================================

func TestNotifyDomainChangeWithCallback(t *testing.T) {
	s := testServer()
	called := false
	s.SetOnDomainChange(func() { called = true })
	s.notifyDomainChange()
	if !called {
		t.Error("onDomainChange callback was not called")
	}
}

func TestNotifyDomainChangeNoCallback(t *testing.T) {
	s := testServer()
	s.notifyDomainChange()
}

// ============================================================================
// maskCloudflareToken
// ============================================================================

// ============================================================================
// tunnelToView
// ============================================================================

func TestTunnelToView(t *testing.T) {
	s := testServer()
	tun := cloudflareTunnel{
		ID:          "tun1",
		Name:        "test-tunnel",
		Hostname:    "test.example.com",
		LocalTarget: "http://localhost:8080",
	}
	view := s.tunnelToView(tun)
	if view.ID != "tun1" {
		t.Errorf("ID = %q, want 'tun1'", view.ID)
	}
	if view.Name != "test-tunnel" {
		t.Errorf("Name = %q, want 'test-tunnel'", view.Name)
	}
	if view.Hostname != "test.example.com" {
		t.Errorf("Hostname = %q, want 'test.example.com'", view.Hostname)
	}
	if view.LocalTarget != "http://localhost:8080" {
		t.Errorf("LocalTarget = %q, want 'http://localhost:8080'", view.LocalTarget)
	}
	if view.Running {
		t.Error("Expected not running")
	}
}

// ============================================================================
// Cloudflare tunnel create/start/stop auth checks
// ============================================================================

func TestHandleCloudflareTunnelCreateNoAuth(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "test-key"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	body := strings.NewReader(`{"name":"test","hostname":"test.example.com","local_target":"http://localhost:8080"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels", body)
	req.Header.Set("Authorization", "Bearer wrong-key")
	s.authMiddleware(requireJSONMiddleware(s.mux)).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCloudflareTunnelStartNoAuth(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "test-key"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/tun1/start", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	s.authMiddleware(requireJSONMiddleware(s.mux)).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCloudflareTunnelStopNoAuth(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "test-key"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/tun1/stop", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	s.authMiddleware(requireJSONMiddleware(s.mux)).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCloudflaredInstallNoAuth(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "test-key"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/cloudflared/install", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	s.authMiddleware(requireJSONMiddleware(s.mux)).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCloudflareConnectNoAuth(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "test-key"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	body := strings.NewReader(`{"token":"t","account_id":"a"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/connect", body)
	req.Header.Set("Authorization", "Bearer wrong-key")
	s.authMiddleware(requireJSONMiddleware(s.mux)).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCloudflareConnectBadJSON(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/connect", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCloudflareConnectMissingFields(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"token":"","account_id":""}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/connect", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCloudflareDisconnect(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/disconnect", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHandleCloudflareCachePurgeNotConnected(t *testing.T) {
	s := testServer()

	body := strings.NewReader("{}")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/cache/purge", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCloudflareStatusNotConnected(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/status", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["connected"] != false {
		t.Errorf("connected = %v, want false", body["connected"])
	}
}

func TestHandleCloudflareTunnelsNotConnected(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/tunnels", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHandleCloudflareTunnelLogsNotFound(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/tunnels/nonexistent/logs", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

// ============================================================================
// handleCloudflareTunnelCreate connected but no token
// ============================================================================

func TestHandleCloudflareTunnelConnectedMissingName(t *testing.T) {
	s := testServer()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Connected: true,
		Token:     "test-token",
		Email:     "test@test.com",
		Tunnels:   []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	t.Cleanup(func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	})

	body := strings.NewReader(`{"name":"","hostname":""}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCloudflareTunnelBadJSON(t *testing.T) {
	s := testServer()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Connected: true,
		Token:     "test-token",
		Email:     "test@test.com",
		Tunnels:   []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	t.Cleanup(func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	})

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCloudflareCachePurgeBadJSON(t *testing.T) {
	s := testServer()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Connected: true,
		Token:     "test-token",
		Email:     "test@test.com",
	}
	cloudflareMu.Unlock()
	t.Cleanup(func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	})

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/cache/purge", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}