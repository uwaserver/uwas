package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) handleSoftwareBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	resp, err := createSoftwareBackup(inst)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "software.backup", inst.Name, true)
	jsonResponse(w, resp)
}

func (s *Server) handleSoftwareBackupAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := listSoftwareInstances()
	if err != nil {
		jsonError(w, "list software: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := softwareBackupAllResponse{Status: "created", Total: len(items)}
	for _, inst := range items {
		item, err := createSoftwareBackup(inst)
		if err != nil {
			item.Status = "failed"
			item.Name = inst.Name
			item.Output = err.Error()
			resp.Failed++
			resp.Status = "completed_with_errors"
		} else if item.Status == "skipped" {
			resp.Skipped++
		} else {
			resp.Created++
		}
		resp.Files = append(resp.Files, item.Files...)
		resp.Items = append(resp.Items, item)
	}
	if len(items) == 0 {
		resp.Status = "skipped"
	}
	s.recordAuditR(r, "software.backup_all", fmt.Sprintf("%d created, %d skipped, %d failed", resp.Created, resp.Skipped, resp.Failed), resp.Failed == 0)
	jsonResponse(w, resp)
}

func createSoftwareBackup(inst softwareInstance) (softwareBackupResponse, error) {
	volumes := collectSoftwareVolumeKeys(inst)
	if len(volumes) == 0 {
		return softwareBackupResponse{Status: "skipped", Name: inst.Name, Files: []string{}}, nil
	}
	backupDir := filepath.Join(softwareBackupRoot, inst.Name)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return softwareBackupResponse{Name: inst.Name}, fmt.Errorf("create backup directory: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	resp := softwareBackupResponse{Status: "created", Name: inst.Name}
	for _, key := range volumes {
		volume := softwareVolumeName(inst, key)
		fileName := sanitizeSoftwareName(inst.Name) + "-" + sanitizeSoftwareName(key) + "-" + stamp + ".tar.gz"
		out, err := runSoftwareDocker("run", "--rm",
			"-v", volume+":/data:ro",
			"-v", backupDir+":/backup",
			"alpine:3.20",
			"sh", "-c", "tar -czf /backup/"+fileName+" -C /data .")
		resp.Output += out
		if err != nil {
			return resp, fmt.Errorf("backup volume %s failed: %w\n%s", volume, err, out)
		}
		resp.Files = append(resp.Files, filepath.Join(backupDir, fileName))
	}
	return resp, nil
}

func updateSoftwareInstance(inst softwareInstance) (softwareUpdateResponse, error) {
	backup, err := createSoftwareBackup(inst)
	if err != nil {
		return softwareUpdateResponse{Name: inst.Name, Status: "failed"}, fmt.Errorf("backup before update failed: %w", err)
	}
	resp := softwareUpdateResponse{
		Status:       "updated",
		Name:         inst.Name,
		BackupStatus: backup.Status,
		BackupFiles:  backup.Files,
		Output:       backup.Output,
	}
	pullOut, err := runSoftwareComposeEnsuringInstalled(inst, "pull")
	resp.PullOutput = pullOut
	resp.Output += pullOut
	if err != nil {
		return resp, fmt.Errorf("docker compose pull failed: %w\n%s", err, pullOut)
	}
	upOut, err := runSoftwareComposeEnsuringInstalled(inst, "up", "-d", "--remove-orphans")
	resp.UpOutput = upOut
	resp.Output += upOut
	if err != nil {
		return resp, fmt.Errorf("docker compose up failed after pull: %w\n%s", err, upOut)
	}
	return resp, nil
}

func (s *Server) handleSoftwareBackupList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	items, err := listSoftwareBackups(inst)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleSoftwareBackupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	backupPath, _, err := resolveSoftwareBackupPath(inst, r.PathValue("backup"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Remove(backupPath); err != nil {
		if os.IsNotExist(err) {
			jsonError(w, "backup not found", http.StatusNotFound)
			return
		}
		jsonError(w, "delete backup: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "software.backup.delete", inst.Name+"/"+filepath.Base(backupPath), true)
	jsonResponse(w, map[string]string{"status": "deleted", "name": inst.Name, "backup": filepath.Base(backupPath)})
}

func (s *Server) handleSoftwareRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req softwareRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	backupPath, volumeKey, err := resolveSoftwareBackupPath(inst, req.Backup)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(backupPath); err != nil {
		jsonError(w, "backup not found: "+err.Error(), http.StatusNotFound)
		return
	}
	volume := softwareVolumeName(inst, volumeKey)
	var output strings.Builder
	if out, err := runSoftwareComposeEnsuringInstalled(inst, "stop"); err != nil {
		jsonError(w, "docker compose stop failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	} else {
		output.WriteString(out)
	}
	backupDir := filepath.Dir(backupPath)
	backupName := filepath.Base(backupPath)
	out, err := runSoftwareDocker("run", "--rm",
		"-v", volume+":/data",
		"-v", backupDir+":/backup:ro",
		"alpine:3.20",
		"sh", "-c", "rm -rf /data/* /data/.[!.]* /data/..?* 2>/dev/null || true; tar -xzf /backup/"+backupName+" -C /data")
	output.WriteString(out)
	if err != nil {
		jsonError(w, "restore volume "+volume+" failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	if out, err := runSoftwareComposeEnsuringInstalled(inst, "up", "-d"); err != nil {
		jsonError(w, "docker compose up failed after restore: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	} else {
		output.WriteString(out)
	}
	s.recordAuditR(r, "software.restore", inst.Name+"/"+backupName, true)
	jsonResponse(w, map[string]string{"status": "restored", "name": inst.Name, "volume": volume, "output": output.String()})
}

func (s *Server) handleSoftwareDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	removeVolumes := r.URL.Query().Get("volumes") == "true"
	var backup softwareBackupResponse
	if removeVolumes {
		backup, err = createSoftwareBackup(inst)
		if err != nil {
			jsonError(w, "backup before volume removal failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	args := []string{"down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "-v")
	}
	out, err := runSoftwareComposeEnsuringInstalled(inst, args...)
	if err != nil {
		if !removeVolumes && isSoftwareComposeMissing(err) {
			if rmErr := removeSoftwareInstanceDir(inst); rmErr != nil {
				jsonError(w, "remove software metadata: "+rmErr.Error(), http.StatusInternalServerError)
				return
			}
			if inst.Domain != "" {
				s.detachSoftwareDomain(inst.Domain, inst.HostPort)
			}
			s.recordAuditR(r, "software.delete", inst.Name, true)
			jsonResponse(w, map[string]any{
				"status": "deleted",
				"name":   inst.Name,
				"output": strings.TrimSpace("Compose was not available, so UWAS removed the failed software record and left any Docker resources untouched.\n" + out),
			})
			return
		}
		jsonError(w, "docker compose down failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	if err := removeSoftwareInstanceDir(inst); err != nil {
		jsonError(w, "remove software metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if inst.Domain != "" {
		s.detachSoftwareDomain(inst.Domain, inst.HostPort)
	}
	s.recordAuditR(r, "software.delete", inst.Name, true)
	resp := map[string]any{"status": "deleted", "name": inst.Name, "output": out}
	if removeVolumes {
		resp["backup_status"] = backup.Status
		resp["backup_files"] = backup.Files
	}
	jsonResponse(w, resp)
}
