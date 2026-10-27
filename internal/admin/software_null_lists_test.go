package admin

// F35300: software library endpoints answered `null` for lists that are empty
// (no instance installed, docker not answering, no backups). The dashboard
// reads `monitor.containers.length` / `monitor.volumes.length` without a
// fallback, so a docker failure while opening an instance's monitor panel threw
// a TypeError. Empty lists must be `[]`.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func softwareNullFixture(t *testing.T) (*Server, softwareInstance) {
	t.Helper()
	root := t.TempDir()
	backupRoot := t.TempDir()
	origRoot, origBackup := softwareLibraryRoot, softwareBackupRoot
	softwareLibraryRoot, softwareBackupRoot = root, backupRoot
	t.Cleanup(func() { softwareLibraryRoot, softwareBackupRoot = origRoot, origBackup })

	dir := filepath.Join(root, "kuma")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name: "kuma", TemplateID: "uptime-kuma", Template: "Uptime Kuma", Dir: dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"), Project: "uwas-kuma",
	}
	if err := os.WriteFile(inst.ComposeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}
	return testServer(), inst
}

func softwareGetJSON(t *testing.T, h func(http.ResponseWriter, *http.Request), path string) map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	h(rec, withAdminContext(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, body: %s", path, rec.Code, rec.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantEmptyArray(t *testing.T, got map[string]json.RawMessage, field string) {
	t.Helper()
	t.Logf("EXPECTED: %s = []\nACTUAL: %s = %s", field, field, got[field])
	if strings.TrimSpace(string(got[field])) != "[]" {
		t.Fatalf("PROBLEM CONFIRMED: %s = %s, want []", field, got[field])
	}
}

func TestSoftwareMonitorEmptyListsAreArrays(t *testing.T) {
	s, _ := softwareNullFixture(t)

	t.Run("docker failing", func(t *testing.T) {
		stubSoftwareCompose(t, "Cannot connect to the Docker daemon", 1)
		got := softwareGetJSON(t, s.handleSoftwareMonitor, "/api/v1/software/kuma/monitor")
		wantEmptyArray(t, got, "containers")
		wantEmptyArray(t, got, "volumes")
	})
	t.Run("containers listed but stats empty", func(t *testing.T) {
		stubSoftwareComposeFunc(t, func(name string, args ...string) string {
			if strings.Contains(strings.Join(args, " "), "ps -q") {
				return "abc123\n"
			}
			return ""
		}, 0)
		got := softwareGetJSON(t, s.handleSoftwareMonitor, "/api/v1/software/kuma/monitor")
		wantEmptyArray(t, got, "containers")
	})
	t.Run("control: no containers running", func(t *testing.T) {
		stubSoftwareCompose(t, "", 0)
		got := softwareGetJSON(t, s.handleSoftwareMonitor, "/api/v1/software/kuma/monitor")
		wantEmptyArray(t, got, "containers")
		wantEmptyArray(t, got, "volumes")
	})
}

func TestSoftwareListsEmptyAreArrays(t *testing.T) {
	s, _ := softwareNullFixture(t)

	t.Run("backups none", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(softwareBackupRoot, "kuma"), 0o755); err != nil {
			t.Fatal(err)
		}
		got := softwareGetJSON(t, s.handleSoftwareBackupList, "/api/v1/software/kuma/backups")
		wantEmptyArray(t, got, "items")
	})
	t.Run("summary and instances with an empty library", func(t *testing.T) {
		softwareLibraryRoot = t.TempDir() // exists, holds no instance
		got := softwareGetJSON(t, s.handleSoftwareMonitorSummary, "/api/v1/software/monitor")
		wantEmptyArray(t, got, "items")
		got = softwareGetJSON(t, s.handleSoftwareInstanceList, "/api/v1/software/instances")
		wantEmptyArray(t, got, "items")
	})
}
