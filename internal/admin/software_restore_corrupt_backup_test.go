package admin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f4710stub runs the `sh -c` script a `docker run` call would run inside the
// alpine container, with /data and /backup mapped to real temp directories.
func f4710stub(t *testing.T, dataDir string) *[]softwareExecCall {
	orig := softwareComposeCommand
	calls := []softwareExecCall{}
	softwareComposeCommand = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, softwareExecCall{Name: name, Args: append([]string(nil), args...)})
		if name == "docker" && len(args) > 0 && args[0] == "run" {
			var backupDir string
			for i, a := range args {
				if a == "-v" && i+1 < len(args) && strings.Contains(args[i+1], ":/backup") {
					backupDir = strings.SplitN(args[i+1], ":", 2)[0]
				}
			}
			script := args[len(args)-1]
			script = strings.ReplaceAll(script, "/backup/", backupDir+"/")
			script = strings.ReplaceAll(script, "/data", dataDir)
			return exec.Command("sh", "-c", script)
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { softwareComposeCommand = orig })
	return &calls
}

func f4710setup(t *testing.T, backupBytes []byte) (*Server, string, string, *[]softwareExecCall) {
	s := testServer()
	root, backupRoot, dataDir := t.TempDir(), t.TempDir(), t.TempDir()
	origRoot, origBackupRoot := softwareLibraryRoot, softwareBackupRoot
	softwareLibraryRoot, softwareBackupRoot = root, backupRoot
	t.Cleanup(func() { softwareLibraryRoot, softwareBackupRoot = origRoot, origBackupRoot })
	calls := f4710stub(t, dataDir)
	dir := filepath.Join(root, "kuma")
	os.MkdirAll(dir, 0755)
	inst := softwareInstance{Name: "kuma", TemplateID: "uptime-kuma", Template: "Uptime Kuma", Category: "Monitoring",
		Dir: dir, ComposeFile: filepath.Join(dir, "docker-compose.yml"), Project: "uwas-kuma", HasWeb: true, HostPort: 3001}
	if err := os.WriteFile(inst.ComposeFile, []byte(composeUptimeKuma(softwareInstallRequest{Name: "kuma", HostPort: 3001}, softwareTemplate{WebPort: 3001})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(backupRoot, "kuma"), 0700)
	name := "kuma-uptime-kuma-data-20260519T010203Z.tar.gz"
	if err := os.WriteFile(filepath.Join(backupRoot, "kuma", name), backupBytes, 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dataDir, "live.db"), []byte("LIVE"), 0600)
	return s, dataDir, name, calls
}

func f4710restore(s *Server, name string) int {
	req := httptest.NewRequest("POST", "/api/v1/software/kuma/restore", strings.NewReader(`{"backup":"`+name+`"}`))
	req.SetPathValue("name", "kuma")
	rec := httptest.NewRecorder()
	s.handleSoftwareRestore(rec, withAdminContext(req))
	return rec.Code
}

// F4710: restore wiped the live volume before the archive was read, so a
// corrupt or truncated backup turned a failed restore into data loss.
func TestSoftwareRestoreCorruptBackupKeepsLiveData(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "restored.db", Mode: 0644, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("BACK"))
	tw.Close()
	gz.Close()
	valid := buf.Bytes()

	// control: a valid archive restores and replaces the live data
	s, dataDir, name, _ := f4710setup(t, valid)
	if code := f4710restore(s, name); code != 200 {
		t.Fatalf("valid restore code = %d", code)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "restored.db")); err != nil {
		t.Fatalf("valid restore did not extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "live.db")); err == nil {
		t.Fatal("valid restore should replace the old contents")
	}

	for label, data := range map[string][]byte{
		"not gzip":  []byte("this is not a gzip tarball"),
		"empty":     {},
		"truncated": valid[:len(valid)/2],
	} {
		t.Run(label, func(t *testing.T) {
			s, dataDir, name, calls := f4710setup(t, data)
			if code := f4710restore(s, name); code != 500 {
				t.Fatalf("restore code = %d, want 500", code)
			}
			if b, err := os.ReadFile(filepath.Join(dataDir, "live.db")); err != nil || string(b) != "LIVE" {
				t.Fatalf("live data lost after failed restore: %q %v", b, err)
			}
			if !softwareCallsContain(calls, "compose -p uwas-kuma", "up -d") {
				t.Fatalf("service was stopped for restore but not restarted after archive validation failed: %#v", *calls)
			}
		})
	}
}
