package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestSoftwareTemplateListIncludesInternalAndWeb(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSoftwareTemplateList(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/software/templates", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var result struct {
		Items []softwareTemplate `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	seen := map[string]softwareTemplate{}
	for _, item := range result.Items {
		seen[item.ID] = item
	}
	if !seen["uptime-kuma"].HasWeb {
		t.Fatalf("uptime-kuma should be a web template")
	}
	if !seen["redis"].Internal || seen["redis"].HasWeb {
		t.Fatalf("redis should be internal-only: %#v", seen["redis"])
	}
	if !seen["postgres"].Internal || !seen["mysql"].Internal || !seen["memcached"].Internal {
		t.Fatalf("expected database/cache internal templates: %#v", seen)
	}
}

func TestSoftwareInstallWritesComposeAndRunsUp(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	calls := stubSoftwareCompose(t, "container-id\n", 0)

	body := strings.NewReader(`{"template_id":"uptime-kuma","name":"My Kuma","host_port":3311,"domain":"status.example.com"}`)
	rec := httptest.NewRecorder()
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var inst softwareInstance
	if err := json.Unmarshal(rec.Body.Bytes(), &inst); err != nil {
		t.Fatal(err)
	}
	if inst.Name != "my-kuma" || inst.HostPort != 3311 || !inst.HasWeb || inst.Domain != "status.example.com" {
		t.Fatalf("unexpected instance: %#v", inst)
	}
	s.configMu.RLock()
	var domainFound bool
	var domain config.Domain
	for _, d := range s.config.Domains {
		if d.Host == "status.example.com" {
			domain = d
			domainFound = true
			break
		}
	}
	s.configMu.RUnlock()
	if !domainFound {
		t.Fatalf("expected status.example.com to be attached: %#v", s.config.Domains)
	}
	if domain.Host != "status.example.com" || domain.Type != "proxy" || domain.SSL.Mode != "auto" {
		t.Fatalf("unexpected attached domain: %#v", domain)
	}
	if !domain.Proxy.AllowPrivateUpstreams || len(domain.Proxy.Upstreams) != 1 || domain.Proxy.Upstreams[0].Address != "http://127.0.0.1:3311" {
		t.Fatalf("unexpected attached proxy config: %#v", domain.Proxy)
	}
	composePath := filepath.Join(root, "my-kuma", "docker-compose.yml")
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "127.0.0.1:3311:3001") {
		t.Fatalf("compose did not bind expected web port:\n%s", string(data))
	}
	if !softwareCallsContain(calls, "compose -p uwas-my-kuma", "up -d") {
		t.Fatalf("compose up was not called: %#v", *calls)
	}
}

func TestSoftwareInstallInternalTemplateIgnoresDomainAndPort(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	stubSoftwareCompose(t, "container-id\n", 0)

	body := strings.NewReader(`{"template_id":"redis","name":"cache","host_port":6379,"domain":"redis.example.com"}`)
	rec := httptest.NewRecorder()
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var inst softwareInstance
	if err := json.Unmarshal(rec.Body.Bytes(), &inst); err != nil {
		t.Fatal(err)
	}
	if inst.HasWeb || inst.HostPort != 0 || inst.Domain != "" {
		t.Fatalf("internal template should not expose web: %#v", inst)
	}
}

func TestSoftwareInstallAutoAllocatesAvailablePort(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	origPortAvailable := softwarePortAvailable
	softwarePortAvailable = func(port int) bool { return port == 3003 }
	t.Cleanup(func() { softwarePortAvailable = origPortAvailable })
	stubSoftwareCompose(t, "ok\n", 0)

	existingDir := filepath.Join(root, "existing-web")
	if err := os.MkdirAll(existingDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(softwareInstance{
		Name:        "existing-web",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         existingDir,
		ComposeFile: filepath.Join(existingDir, "docker-compose.yml"),
		Project:     "uwas-existing-web",
		HasWeb:      true,
		HostPort:    3001,
	}); err != nil {
		t.Fatal(err)
	}
	s.configMu.Lock()
	s.config.Domains = append(s.config.Domains, config.Domain{
		Host:  "reserved.example.com",
		Type:  "proxy",
		Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:3002"}}},
	})
	s.configMu.Unlock()

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"template_id":"uptime-kuma","name":"auto-web"}`)
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var inst softwareInstance
	if err := json.Unmarshal(rec.Body.Bytes(), &inst); err != nil {
		t.Fatal(err)
	}
	if inst.HostPort != 3003 {
		t.Fatalf("host port = %d, want 3003", inst.HostPort)
	}
	data, err := os.ReadFile(filepath.Join(root, "auto-web", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "127.0.0.1:3003:3001") {
		t.Fatalf("compose did not use auto port:\n%s", string(data))
	}
}

func TestSoftwareInstallRejectsReservedPortBeforeCompose(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	calls := stubSoftwareCompose(t, "ok\n", 0)

	existingDir := filepath.Join(root, "existing-web")
	if err := os.MkdirAll(existingDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(softwareInstance{
		Name:        "existing-web",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         existingDir,
		ComposeFile: filepath.Join(existingDir, "docker-compose.yml"),
		Project:     "uwas-existing-web",
		HasWeb:      true,
		HostPort:    3311,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"template_id":"uptime-kuma","name":"conflict-web","host_port":3311}`)
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body: %s", rec.Code, rec.Body.String())
	}
	if softwareCallsContain(calls, "compose -p uwas-conflict-web") {
		t.Fatalf("compose should not run on port conflict: %#v", *calls)
	}
}

func TestSoftwarePortCheckSuggestsAndExplainsConflicts(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	origPortAvailable := softwarePortAvailable
	softwarePortAvailable = func(port int) bool { return port != 3004 }
	t.Cleanup(func() { softwarePortAvailable = origPortAvailable })

	existingDir := filepath.Join(root, "existing-web")
	if err := os.MkdirAll(existingDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(softwareInstance{
		Name:        "existing-web",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         existingDir,
		ComposeFile: filepath.Join(existingDir, "docker-compose.yml"),
		Project:     "uwas-existing-web",
		HasWeb:      true,
		HostPort:    3001,
	}); err != nil {
		t.Fatal(err)
	}
	s.configMu.Lock()
	s.config.Domains = append(s.config.Domains, config.Domain{
		Host: "api.local",
		Type: "proxy",
		Locations: []config.LocationConfig{
			{Match: "/api/", ProxyPass: "http://127.0.0.1:3002"},
		},
	})
	s.configMu.Unlock()

	rec := httptest.NewRecorder()
	s.handleSoftwarePortCheck(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/software/ports/check?default_port=3001", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var check softwarePortCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if !check.Available || check.SuggestedPort != 3003 {
		t.Fatalf("check = %#v, want suggested 3003", check)
	}

	rec = httptest.NewRecorder()
	s.handleSoftwarePortCheck(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/software/ports/check?port=3002", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("conflict status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if check.Available || !strings.Contains(check.Reason, "domain location") || check.SuggestedPort != 3003 {
		t.Fatalf("conflict check = %#v", check)
	}

	rec = httptest.NewRecorder()
	s.handleSoftwarePortCheck(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/software/ports/check?port=70000", nil)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad port status = %d, want 400", rec.Code)
	}
}

func TestSoftwareDeleteRunsDownRemovesMetadataAndAttachedDomain(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	calls := stubSoftwareCompose(t, "ok\n", 0)

	body := strings.NewReader(`{"template_id":"uptime-kuma","name":"delete-me","host_port":3322,"domain":"delete.example.com"}`)
	rec := httptest.NewRecorder()
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("install status = %d, body: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest("DELETE", "/api/v1/software/delete-me", nil)
	req.SetPathValue("name", "delete-me")
	rec = httptest.NewRecorder()
	s.handleSoftwareDelete(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !softwareCallsContain(calls, "compose -p uwas-delete-me", "down --remove-orphans") {
		t.Fatalf("compose down was not called: %#v", *calls)
	}
	if _, err := os.Stat(filepath.Join(root, "delete-me")); !os.IsNotExist(err) {
		t.Fatalf("software directory should be removed, stat err=%v", err)
	}
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	for _, d := range s.config.Domains {
		if d.Host == "delete.example.com" {
			t.Fatalf("attached software domain should be removed: %#v", d)
		}
	}
}

func TestSoftwareDeleteWithVolumesBacksUpBeforeDown(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	backupRoot := t.TempDir()
	origRoot := softwareLibraryRoot
	origBackupRoot := softwareBackupRoot
	softwareLibraryRoot = root
	softwareBackupRoot = backupRoot
	t.Cleanup(func() {
		softwareLibraryRoot = origRoot
		softwareBackupRoot = origBackupRoot
	})
	calls := stubSoftwareCompose(t, "ok\n", 0)

	dir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "kuma",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-kuma",
		HasWeb:      true,
		HostPort:    3001,
	}
	if err := os.WriteFile(inst.ComposeFile, []byte(composeUptimeKuma(softwareInstallRequest{Name: inst.Name, HostPort: 3001}, softwareTemplate{WebPort: 3001})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/api/v1/software/kuma?volumes=true", nil)
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	s.handleSoftwareDelete(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !softwareCallsContain(calls, "docker run --rm", "uwas-kuma_uptime-kuma-data:/data:ro") {
		t.Fatalf("backup should run before removing volumes: %#v", *calls)
	}
	if !softwareCallsContain(calls, "compose -p uwas-kuma", "down --remove-orphans -v") {
		t.Fatalf("compose down -v was not called: %#v", *calls)
	}
	if !strings.Contains(rec.Body.String(), `"backup_status":"created"`) {
		t.Fatalf("delete response should include backup status: %s", rec.Body.String())
	}
}

func TestSoftwareDeleteWithVolumesStopsWhenBackupFails(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	backupRoot := t.TempDir()
	origRoot := softwareLibraryRoot
	origBackupRoot := softwareBackupRoot
	softwareLibraryRoot = root
	softwareBackupRoot = backupRoot
	t.Cleanup(func() {
		softwareLibraryRoot = origRoot
		softwareBackupRoot = origBackupRoot
	})
	calls := stubSoftwareCompose(t, "backup failed\n", 1)

	dir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "kuma",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-kuma",
	}
	if err := os.WriteFile(inst.ComposeFile, []byte(composeUptimeKuma(softwareInstallRequest{Name: inst.Name, HostPort: 3001}, softwareTemplate{WebPort: 3001})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/api/v1/software/kuma?volumes=true", nil)
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	s.handleSoftwareDelete(rec, withAdminContext(req))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 500, body: %s", rec.Code, rec.Body.String())
	}
	if softwareCallsContain(calls, "compose -p uwas-kuma", "down --remove-orphans -v") {
		t.Fatalf("compose down -v should not run after backup failure: %#v", *calls)
	}
	if _, err := os.Stat(filepath.Join(root, "kuma", "uwas-software.yaml")); err != nil {
		t.Fatalf("metadata should remain after backup failure: %v", err)
	}
}

func TestSoftwareUpdateBacksUpPullsAndRecreates(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	backupRoot := t.TempDir()
	origRoot := softwareLibraryRoot
	origBackupRoot := softwareBackupRoot
	softwareLibraryRoot = root
	softwareBackupRoot = backupRoot
	t.Cleanup(func() {
		softwareLibraryRoot = origRoot
		softwareBackupRoot = origBackupRoot
	})
	calls := stubSoftwareCompose(t, "ok\n", 0)

	dir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "kuma",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-kuma",
		HasWeb:      true,
		HostPort:    3001,
	}
	if err := os.WriteFile(inst.ComposeFile, []byte(composeUptimeKuma(softwareInstallRequest{Name: inst.Name, HostPort: 3001}, softwareTemplate{WebPort: 3001})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/v1/software/kuma/update", nil)
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	s.handleSoftwareUpdate(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp softwareUpdateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "updated" || resp.BackupStatus != "created" || len(resp.BackupFiles) != 1 {
		t.Fatalf("unexpected update response: %#v", resp)
	}
	if !softwareCallsContain(calls, "docker run --rm", "uwas-kuma_uptime-kuma-data:/data:ro") ||
		!softwareCallsContain(calls, "compose -p uwas-kuma", "pull") ||
		!softwareCallsContain(calls, "compose -p uwas-kuma", "up -d --remove-orphans") {
		t.Fatalf("update calls missing: %#v", *calls)
	}
}

func TestSoftwareUpdateAllContinuesAfterFailures(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	backupRoot := t.TempDir()
	origRoot := softwareLibraryRoot
	origBackupRoot := softwareBackupRoot
	softwareLibraryRoot = root
	softwareBackupRoot = backupRoot
	t.Cleanup(func() {
		softwareLibraryRoot = origRoot
		softwareBackupRoot = origBackupRoot
	})

	var mu sync.Mutex
	calls := []softwareExecCall{}
	orig := softwareComposeCommand
	softwareComposeCommand = func(name string, args ...string) *exec.Cmd {
		mu.Lock()
		calls = append(calls, softwareExecCall{Name: name, Args: append([]string(nil), args...)})
		mu.Unlock()
		mode := "ok"
		output := "ok\n"
		joined := name + " " + strings.Join(args, " ")
		if strings.Contains(joined, "compose -p uwas-bad") && strings.Contains(joined, "pull") {
			mode = "fail"
			output = "pull failed\n"
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestSoftwareComposeHelperProcess", "--", "--software-compose-helper", mode)
		cmd.Env = append(os.Environ(), "UWAS_SOFTWARE_HELPER_OUTPUT="+output)
		return cmd
	}
	t.Cleanup(func() { softwareComposeCommand = orig })

	for _, name := range []string{"bad", "good"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		inst := softwareInstance{
			Name:        name,
			TemplateID:  "memcached",
			Template:    "Memcached",
			Category:    "Infrastructure",
			Dir:         dir,
			ComposeFile: filepath.Join(dir, "docker-compose.yml"),
			Project:     "uwas-" + name,
		}
		if err := os.WriteFile(inst.ComposeFile, []byte(composeMemcached(softwareInstallRequest{Name: inst.Name}, softwareTemplate{})), 0600); err != nil {
			t.Fatal(err)
		}
		if err := saveSoftwareInstance(inst); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	s.handleSoftwareUpdateAll(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/updates", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update all status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp softwareUpdateAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "completed_with_errors" || resp.Total != 2 || resp.Updated != 1 || resp.Failed != 1 {
		t.Fatalf("unexpected update all response: %#v", resp)
	}
	if !softwareCallsContain(&calls, "compose -p uwas-good", "up -d --remove-orphans") {
		t.Fatalf("good instance should still update after bad pull failure: %#v", calls)
	}
	if softwareCallsContain(&calls, "compose -p uwas-bad", "up -d --remove-orphans") {
		t.Fatalf("failed pull should not recreate bad instance: %#v", calls)
	}
}

func TestSoftwareInstanceListLifecycleAndLogs(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	calls := stubSoftwareCompose(t, "container-id\n", 0)

	dir := filepath.Join(root, "worker-cache")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "worker-cache",
		TemplateID:  "redis",
		Template:    "Redis",
		Category:    "Infrastructure",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-worker-cache",
	}
	if err := os.WriteFile(inst.ComposeFile, []byte(composeRedis(softwareInstallRequest{Name: inst.Name}, softwareTemplate{})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.handleSoftwareInstanceList(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/software/instances", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Items []softwareInstance `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].Status != "running" {
		t.Fatalf("unexpected list response: %#v", listed.Items)
	}

	for _, tc := range []struct {
		name   string
		method func(http.ResponseWriter, *http.Request)
		want   string
	}{
		{"start", s.handleSoftwareStart, "up -d"},
		{"stop", s.handleSoftwareStop, "stop"},
		{"restart", s.handleSoftwareRestart, "restart"},
	} {
		req := httptest.NewRequest("POST", "/api/v1/software/worker-cache/"+tc.name, nil)
		req.SetPathValue("name", "worker-cache")
		rec := httptest.NewRecorder()
		tc.method(rec, withAdminContext(req))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body: %s", tc.name, rec.Code, rec.Body.String())
		}
		if !softwareCallsContain(calls, "compose -p uwas-worker-cache", tc.want) {
			t.Fatalf("%s compose call not found: %#v", tc.name, *calls)
		}
	}

	req := httptest.NewRequest("GET", "/api/v1/software/worker-cache/logs", nil)
	req.SetPathValue("name", "worker-cache")
	rec = httptest.NewRecorder()
	s.handleSoftwareLogs(rec, withAdminContext(req))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "container-id") {
		t.Fatalf("logs response = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !softwareCallsContain(calls, "compose -p uwas-worker-cache", "logs --tail 200") {
		t.Fatalf("logs compose call not found: %#v", *calls)
	}
}

func TestSoftwareMonitorAndBackupVolumes(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	backupRoot := t.TempDir()
	origRoot := softwareLibraryRoot
	origBackupRoot := softwareBackupRoot
	softwareLibraryRoot = root
	softwareBackupRoot = backupRoot
	t.Cleanup(func() {
		softwareLibraryRoot = origRoot
		softwareBackupRoot = origBackupRoot
	})
	calls := stubSoftwareComposeFunc(t, func(name string, args ...string) string {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "compose -p uwas-kuma -f") && strings.Contains(joined, "ps -q"):
			return "abc123\n"
		case strings.Contains(joined, "compose -p uwas-kuma -f") && strings.Contains(joined, "ps --format json"):
			return `{"ID":"abc123","Name":"uwas-kuma-uptime-kuma-1","Service":"uptime-kuma","State":"running"}` + "\n"
		case strings.Contains(joined, "stats --no-stream"):
			return `{"Container":"abc123","Name":"uwas-kuma-uptime-kuma-1","CPUPerc":"1.25%","MemUsage":"12MiB / 1GiB","MemPerc":"1.17%","NetIO":"2kB / 4kB","BlockIO":"8kB / 16kB","PIDs":"7"}` + "\n"
		case strings.Contains(joined, "top abc123"):
			return "UID PID PPID C STIME TTY TIME CMD\nroot 101 1 0 12:00 ? 00:00:01 node server.js --port 3001\n"
		case strings.Contains(joined, "volume inspect uwas-kuma_uptime-kuma-data"):
			return `[{"Name":"uwas-kuma_uptime-kuma-data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/uwas-kuma_uptime-kuma-data/_data","Scope":"local"}]`
		case strings.Contains(joined, "run --rm"):
			return "backup ok\n"
		default:
			return ""
		}
	}, 0)

	dir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "kuma",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-kuma",
		HasWeb:      true,
		HostPort:    3001,
	}
	if err := os.WriteFile(inst.ComposeFile, []byte(composeUptimeKuma(softwareInstallRequest{Name: inst.Name, HostPort: 3001}, softwareTemplate{WebPort: 3001})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/v1/software/kuma/monitor", nil)
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	s.handleSoftwareMonitor(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("monitor status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var mon softwareMonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mon); err != nil {
		t.Fatal(err)
	}
	if len(mon.Containers) != 1 || mon.TotalCPUPct != 1.25 || mon.Containers[0].MemoryUsage != 12*1024*1024 {
		t.Fatalf("unexpected monitor response: %#v", mon)
	}
	if len(mon.Volumes) != 1 || mon.Volumes[0].Name != "uwas-kuma_uptime-kuma-data" || mon.Volumes[0].Driver != "local" {
		t.Fatalf("unexpected volumes: %#v", mon.Volumes)
	}

	req = httptest.NewRequest("GET", "/api/v1/software/monitor", nil)
	rec = httptest.NewRecorder()
	s.handleSoftwareMonitorSummary(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var summary softwareMonitorSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ContainerCount != 1 || summary.VolumeCount != 1 || summary.TotalMemory != 12*1024*1024 {
		t.Fatalf("unexpected monitor summary: %#v", summary)
	}

	req = httptest.NewRequest("GET", "/api/v1/software/kuma/processes", nil)
	req.SetPathValue("name", "kuma")
	rec = httptest.NewRecorder()
	s.handleSoftwareProcesses(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("processes status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var procList struct {
		Items []softwareProcessInfo `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &procList); err != nil {
		t.Fatal(err)
	}
	if len(procList.Items) != 1 || procList.Items[0].Service != "uptime-kuma" || procList.Items[0].PID != "101" || !strings.Contains(procList.Items[0].Command, "node server.js") {
		t.Fatalf("unexpected process list: %#v", procList.Items)
	}

	req = httptest.NewRequest("POST", "/api/v1/software/kuma/backup", nil)
	req.SetPathValue("name", "kuma")
	rec = httptest.NewRecorder()
	s.handleSoftwareBackup(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("backup status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var backup softwareBackupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	if backup.Status != "created" || len(backup.Files) != 1 || !strings.Contains(backup.Files[0], filepath.Join(backupRoot, "kuma")) {
		t.Fatalf("unexpected backup response: %#v", backup)
	}
	if !softwareCallsContain(calls, "docker run --rm", "uwas-kuma_uptime-kuma-data:/data:ro") {
		t.Fatalf("docker volume backup was not called: %#v", *calls)
	}

	req = httptest.NewRequest("POST", "/api/v1/software/backups", nil)
	rec = httptest.NewRecorder()
	s.handleSoftwareBackupAll(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("backup all status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var backupAll softwareBackupAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &backupAll); err != nil {
		t.Fatal(err)
	}
	if backupAll.Status != "created" || backupAll.Total != 1 || backupAll.Created != 1 || len(backupAll.Files) != 1 {
		t.Fatalf("unexpected backup all response: %#v", backupAll)
	}

	backupFile := filepath.Join(backupRoot, "kuma", "kuma-uptime-kuma-data-20260519T010203Z.tar.gz")
	if err := os.WriteFile(backupFile, []byte("backup"), 0600); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", "/api/v1/software/kuma/backups", nil)
	req.SetPathValue("name", "kuma")
	rec = httptest.NewRecorder()
	s.handleSoftwareBackupList(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("backup list status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []softwareBackupInfo `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) == 0 || list.Items[0].VolumeKey != "uptime-kuma-data" {
		t.Fatalf("unexpected backup list: %#v", list.Items)
	}

	req = httptest.NewRequest("POST", "/api/v1/software/kuma/restore", strings.NewReader(`{"backup":"kuma-uptime-kuma-data-20260519T010203Z.tar.gz"}`))
	req.SetPathValue("name", "kuma")
	rec = httptest.NewRecorder()
	s.handleSoftwareRestore(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !softwareCallsContain(calls, "compose -p uwas-kuma", "stop") ||
		!softwareCallsContain(calls, "docker run --rm", "uwas-kuma_uptime-kuma-data:/data") ||
		!softwareCallsContain(calls, "compose -p uwas-kuma", "up -d") {
		t.Fatalf("restore calls missing: %#v", *calls)
	}

	req = httptest.NewRequest("DELETE", "/api/v1/software/kuma/backups/kuma-uptime-kuma-data-20260519T010203Z.tar.gz", nil)
	req.SetPathValue("name", "kuma")
	req.SetPathValue("backup", "kuma-uptime-kuma-data-20260519T010203Z.tar.gz")
	rec = httptest.NewRecorder()
	s.handleSoftwareBackupDelete(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("backup delete status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(backupFile); !os.IsNotExist(err) {
		t.Fatalf("backup file should be deleted, err=%v", err)
	}
}

func TestSoftwareBackupAllReportsSkippedAndFailedItems(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	backupRoot := t.TempDir()
	origRoot := softwareLibraryRoot
	origBackupRoot := softwareBackupRoot
	softwareLibraryRoot = root
	softwareBackupRoot = backupRoot
	t.Cleanup(func() {
		softwareLibraryRoot = origRoot
		softwareBackupRoot = origBackupRoot
	})
	calls := stubSoftwareCompose(t, "backup failed\n", 1)

	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	cache := softwareInstance{
		Name:        "cache",
		TemplateID:  "memcached",
		Template:    "Memcached",
		Category:    "Infrastructure",
		Dir:         cacheDir,
		ComposeFile: filepath.Join(cacheDir, "docker-compose.yml"),
		Project:     "uwas-cache",
	}
	if err := os.WriteFile(cache.ComposeFile, []byte(composeMemcached(softwareInstallRequest{Name: cache.Name}, softwareTemplate{})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(cache); err != nil {
		t.Fatal(err)
	}

	kumaDir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(kumaDir, 0755); err != nil {
		t.Fatal(err)
	}
	kuma := softwareInstance{
		Name:        "kuma",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         kumaDir,
		ComposeFile: filepath.Join(kumaDir, "docker-compose.yml"),
		Project:     "uwas-kuma",
		HasWeb:      true,
		HostPort:    3001,
	}
	if err := os.WriteFile(kuma.ComposeFile, []byte(composeUptimeKuma(softwareInstallRequest{Name: kuma.Name, HostPort: 3001}, softwareTemplate{WebPort: 3001})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(kuma); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.handleSoftwareBackupAll(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/backups", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp softwareBackupAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "completed_with_errors" || resp.Total != 2 || resp.Skipped != 1 || resp.Failed != 1 || resp.Created != 0 {
		t.Fatalf("unexpected backup all response: %#v", resp)
	}
	if len(resp.Items) != 2 || resp.Items[0].Status != "skipped" || resp.Items[1].Status != "failed" {
		t.Fatalf("unexpected backup all items: %#v", resp.Items)
	}
	if !strings.Contains(resp.Items[1].Output, "backup volume uwas-kuma_uptime-kuma-data failed") {
		t.Fatalf("failure output should include volume context: %#v", resp.Items[1])
	}
	if !softwareCallsContain(calls, "docker run --rm", "uwas-kuma_uptime-kuma-data:/data:ro") {
		t.Fatalf("docker backup was not attempted: %#v", *calls)
	}
}

func TestRunSoftwareComposeFallsBackToLegacyDockerCompose(t *testing.T) {
	dir := t.TempDir()
	inst := softwareInstance{
		Name:        "legacy",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-legacy",
	}
	if err := os.WriteFile(inst.ComposeFile, []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	calls := []softwareExecCall{}
	orig := softwareComposeCommand
	softwareComposeCommand = func(name string, args ...string) *exec.Cmd {
		mu.Lock()
		calls = append(calls, softwareExecCall{Name: name, Args: append([]string(nil), args...)})
		mu.Unlock()
		mode := "ok"
		output := "legacy ok\n"
		if name == "docker" {
			mode = "fail"
			output = "unknown shorthand flag: 'p' in -p\nUsage: docker [OPTIONS] COMMAND [ARG...]\n"
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestSoftwareComposeHelperProcess", "--", "--software-compose-helper", mode)
		cmd.Env = append(os.Environ(), "UWAS_SOFTWARE_HELPER_OUTPUT="+output)
		return cmd
	}
	t.Cleanup(func() { softwareComposeCommand = orig })

	out, err := runSoftwareCompose(inst, "up", "-d")
	if err != nil {
		t.Fatalf("runSoftwareCompose error = %v, output: %s", err, out)
	}
	if out != "legacy ok\n" {
		t.Fatalf("output = %q, want legacy ok", out)
	}
	if !softwareCallsContain(&calls, "docker compose -p uwas-legacy", "up -d") {
		t.Fatalf("docker compose should be attempted first: %#v", calls)
	}
	if !softwareCallsContain(&calls, "docker-compose -p uwas-legacy", "up -d") {
		t.Fatalf("docker-compose fallback should be used: %#v", calls)
	}
}

func TestSoftwareComposeTemplatesAndSecrets(t *testing.T) {
	cases := []struct {
		id       string
		contains []string
	}{
		{"n8n", []string{"image: n8nio/n8n:latest", "WEBHOOK_URL=https://flow.example.com/", "N8N_BASIC_AUTH_PASSWORD="}},
		{"vaultwarden", []string{"image: vaultwarden/server:latest", "127.0.0.1:8088:80"}},
		{"gitea", []string{"image: gitea/gitea:latest", "127.0.0.1:3000:3000"}},
		{"adminer-postgres", []string{"image: postgres:16-alpine", "image: adminer:latest", "127.0.0.1:8081:8080"}},
		{"postgres", []string{"image: postgres:16-alpine", "POSTGRES_PASSWORD="}},
		{"mysql", []string{"image: mysql:8", "MYSQL_ROOT_PASSWORD=", "MYSQL_PASSWORD="}},
		{"mariadb", []string{"image: mariadb:11", "MARIADB_ROOT_PASSWORD=", "MARIADB_PASSWORD="}},
		{"minio", []string{"image: minio/minio:latest", "MINIO_ROOT_PASSWORD="}},
		{"memcached", []string{"image: memcached:1.6-alpine", "memcached\", \"-m\", \"128"}},
	}
	for _, tc := range cases {
		tpl := findSoftwareTemplate(tc.id)
		if tpl == nil {
			t.Fatalf("missing template %s", tc.id)
		}
		req := softwareInstallRequest{
			TemplateID: tc.id,
			Name:       "demo",
			HostPort:   tpl.DefaultPort,
			Domain:     "flow.example.com",
			Env:        map[string]string{},
		}
		fillSoftwareSecrets(tc.id, req.Env)
		compose := tpl.compose(req, *tpl)
		for _, want := range tc.contains {
			if !strings.Contains(compose, want) {
				t.Fatalf("%s compose missing %q:\n%s", tc.id, want, compose)
			}
		}
	}
}

func TestSoftwareInstallRejectsBadInput(t *testing.T) {
	s := testServer()
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })
	origPortAvailable := softwarePortAvailable
	softwarePortAvailable = func(int) bool { return true }
	t.Cleanup(func() { softwarePortAvailable = origPortAvailable })

	for _, body := range []string{
		`{"template_id":"uptime-kuma","name":"x","host_port":70000}`,
		`{"template_id":"uptime-kuma","name":"x","host_port":3001,"domain":"bad host"}`,
	} {
		rec := httptest.NewRecorder()
		s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", strings.NewReader(body))))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", body, rec.Code)
		}
	}
}

func TestSoftwareInstallRejectsUnknownTemplate(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"template_id":"not-real","name":"x"}`)
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/software/install", body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

type softwareExecCall struct {
	Name string
	Args []string
}

func TestSoftwareComposeHelperProcess(t *testing.T) {
	idx := -1
	for i, arg := range os.Args {
		if arg == "--software-compose-helper" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	code := 0
	if idx+1 < len(os.Args) && os.Args[idx+1] == "fail" {
		code = 1
	}
	fmt.Fprint(os.Stdout, os.Getenv("UWAS_SOFTWARE_HELPER_OUTPUT"))
	os.Exit(code)
}

func stubSoftwareCompose(t *testing.T, output string, exitCode int) *[]softwareExecCall {
	t.Helper()
	return stubSoftwareComposeFunc(t, func(string, ...string) string { return output }, exitCode)
}

func stubSoftwareComposeFunc(t *testing.T, output func(string, ...string) string, exitCode int) *[]softwareExecCall {
	t.Helper()
	var mu sync.Mutex
	calls := []softwareExecCall{}
	orig := softwareComposeCommand
	softwareComposeCommand = func(name string, args ...string) *exec.Cmd {
		mu.Lock()
		calls = append(calls, softwareExecCall{Name: name, Args: append([]string(nil), args...)})
		mu.Unlock()
		mode := "ok"
		if exitCode != 0 {
			mode = "fail"
		}
		cmdArgs := []string{"-test.run=TestSoftwareComposeHelperProcess", "--", "--software-compose-helper", mode}
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "UWAS_SOFTWARE_HELPER_OUTPUT="+output(name, args...))
		return cmd
	}
	t.Cleanup(func() { softwareComposeCommand = orig })
	return &calls
}

func softwareCallsContain(calls *[]softwareExecCall, parts ...string) bool {
	for _, call := range *calls {
		joined := call.Name + " " + strings.Join(call.Args, " ")
		ok := true
		for _, part := range parts {
			if !strings.Contains(joined, part) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
