package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func runSoftwareCompose(inst softwareInstance, args ...string) (string, error) {
	if err := ensureSoftwareComposeFileCompatible(inst); err != nil {
		return "", err
	}
	composeArgs := []string{"-p", inst.Project, "-f", inst.ComposeFile}
	composeArgs = append(composeArgs, args...)

	out, err := runSoftwareCommand(inst.Dir, "docker", append([]string{"compose"}, composeArgs...)...)
	if err == nil || (!isCommandNotFound(err) && !shouldFallbackDockerCompose(out)) {
		return out, err
	}
	fallbackOut, fallbackErr := runSoftwareCommand(inst.Dir, "docker-compose", composeArgs...)
	if fallbackErr == nil {
		return fallbackOut, nil
	}
	if isCommandNotFound(err) && isCommandNotFound(fallbackErr) {
		return "", softwareComposeMissingError{Reason: "Docker and Docker Compose are not installed or not available in PATH"}
	}
	if isCommandNotFound(fallbackErr) || shouldFallbackDockerCompose(fallbackOut) {
		return strings.TrimSpace(out + fallbackOut), softwareComposeMissingError{Reason: "Docker Compose is not installed or not available in PATH"}
	}
	return out + fallbackOut, fallbackErr
}

func ensureSoftwareComposeFileCompatible(inst softwareInstance) error {
	if strings.TrimSpace(inst.ComposeFile) == "" {
		return nil
	}
	data, err := os.ReadFile(inst.ComposeFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read docker-compose.yml: %w", err)
	}
	cleaned, changed := stripTopLevelComposeName(data)
	if !changed {
		return nil
	}
	if err := os.WriteFile(inst.ComposeFile, cleaned, 0600); err != nil {
		return fmt.Errorf("rewrite docker-compose.yml for legacy Compose compatibility: %w", err)
	}
	return nil
}

func stripTopLevelComposeName(data []byte) ([]byte, bool) {
	lines := strings.SplitAfter(string(data), "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}
		if strings.HasPrefix(trimmed, "name:") {
			changed = true
			continue
		}
		out = append(out, line)
	}
	if !changed {
		return data, false
	}
	return []byte(strings.Join(out, "")), true
}

func runSoftwareComposeEnsuringInstalled(inst softwareInstance, args ...string) (string, error) {
	out, err := runSoftwareCompose(inst, args...)
	if err == nil || !isSoftwareComposeMissing(err) {
		return out, err
	}
	installOut, installErr := ensureSoftwareComposeAvailable()
	combined := strings.TrimSpace(installOut + "\n" + out)
	if installErr != nil {
		return combined, installErr
	}
	retryOut, retryErr := runSoftwareCompose(inst, args...)
	if combined != "" && retryOut != "" {
		retryOut = combined + "\n" + retryOut
	} else if combined != "" {
		retryOut = combined
	}
	return retryOut, retryErr
}

type softwareComposeMissingError struct {
	Reason string
}

func (e softwareComposeMissingError) Error() string {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "Docker Compose is not installed or not available in PATH"
	}
	return reason + "; UWAS tried to use `docker compose` and legacy `docker-compose`. Install Docker + Compose, or let UWAS install them automatically on Debian/Ubuntu with: apt-get update && apt-get install -y docker.io docker-compose-plugin"
}

func isSoftwareComposeMissing(err error) bool {
	var missing softwareComposeMissingError
	return errors.As(err, &missing)
}

func ensureSoftwareComposeAvailable() (string, error) {
	softwareComposeSetupMu.Lock()
	defer softwareComposeSetupMu.Unlock()

	if out, err := probeSoftwareCompose(); err == nil {
		return out, nil
	}
	if _, err := softwareLookPath("apt-get"); err != nil {
		return "", softwareComposeMissingError{Reason: "Docker Compose is missing and apt-get is not available for automatic install"}
	}

	cmd := softwareComposeCommand("sh", "-c", strings.Join([]string{
		"apt-get update",
		"(apt-get install -y docker.io docker-compose-plugin || apt-get install -y docker.io docker-compose)",
		"(systemctl enable --now docker >/dev/null 2>&1 || service docker start >/dev/null 2>&1 || true)",
	}, " && "))
	cmd.Env = append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"NEEDRESTART_MODE=a",
		"APT_LISTCHANGES_FRONTEND=none",
		"DEBIAN_PRIORITY=critical",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("automatic Docker Compose install failed: %w", err)
	}
	probeOut, probeErr := probeSoftwareCompose()
	if probeErr != nil {
		return string(out) + probeOut, fmt.Errorf("docker Compose install finished but Compose is still unavailable: %w", probeErr)
	}
	return string(out) + probeOut, nil
}

func probeSoftwareCompose() (string, error) {
	out, err := runSoftwareCommand("", "docker", "compose", "version")
	if err == nil {
		return out, nil
	}
	fallbackOut, fallbackErr := runSoftwareCommand("", "docker-compose", "version")
	if fallbackErr == nil {
		return fallbackOut, nil
	}
	if isCommandNotFound(err) && isCommandNotFound(fallbackErr) {
		return "", softwareComposeMissingError{Reason: "Docker and Docker Compose are not installed or not available in PATH"}
	}
	if isCommandNotFound(fallbackErr) || shouldFallbackDockerCompose(out) || shouldFallbackDockerCompose(fallbackOut) {
		return strings.TrimSpace(out + fallbackOut), softwareComposeMissingError{Reason: "Docker Compose is not installed or not available in PATH"}
	}
	return out + fallbackOut, fallbackErr
}

func runSoftwareDocker(args ...string) (string, error) {
	return runSoftwareCommand("", "docker", args...)
}

func runSoftwareCommand(dir, name string, args ...string) (string, error) {
	cmd := softwareComposeCommand(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func isCommandNotFound(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)
}

func shouldFallbackDockerCompose(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "unknown shorthand flag: 'p'") ||
		strings.Contains(output, "unknown shorthand flag: \"p\"") ||
		strings.Contains(output, "docker: 'compose' is not a docker command") ||
		strings.Contains(output, "docker: \"compose\" is not a docker command") ||
		strings.Contains(output, "is not a docker command")
}

func collectSoftwareContainerStats(inst softwareInstance) ([]softwareContainerStat, error) {
	idsOut, err := runSoftwareCompose(inst, "ps", "-q")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(idsOut)
	if len(ids) == 0 {
		return []softwareContainerStat{}, nil
	}
	meta := collectSoftwareContainerMeta(inst)
	args := []string{"stats", "--no-stream", "--format", "{{json .}}"}
	args = append(args, ids...)
	statsOut, err := runSoftwareDocker(args...)
	if err != nil {
		return nil, err
	}
	var out []softwareContainerStat
	for _, line := range strings.Split(statsOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw map[string]string
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		id := raw["Container"]
		if id == "" {
			id = raw["ID"]
		}
		stat := softwareContainerStat{
			ID:        id,
			Name:      raw["Name"],
			CPUPct:    parseDockerPercent(raw["CPUPerc"]),
			MemoryPct: parseDockerPercent(raw["MemPerc"]),
			PIDs:      parseDockerInt(raw["PIDs"]),
		}
		stat.MemoryUsage, stat.MemoryLimit = parseDockerPair(raw["MemUsage"])
		stat.NetworkInput, stat.NetworkOutput = parseDockerPair(raw["NetIO"])
		stat.BlockInput, stat.BlockOutput = parseDockerPair(raw["BlockIO"])
		if m, ok := meta[stat.Name]; ok {
			stat.Service = m.Service
			stat.State = m.State
		}
		out = append(out, stat)
	}
	return out, nil
}

func collectSoftwareMonitor(inst softwareInstance) softwareMonitorResponse {
	resp := softwareMonitorResponse{Instance: inst}
	containers, err := collectSoftwareContainerStats(inst)
	if err == nil {
		resp.Containers = containers
	}
	resp.Volumes = collectSoftwareVolumes(inst)
	for _, c := range resp.Containers {
		resp.TotalCPUPct += c.CPUPct
		resp.TotalMemory += c.MemoryUsage
		resp.TotalMemoryLimit += c.MemoryLimit
		resp.TotalNetworkIn += c.NetworkInput
		resp.TotalNetworkOut += c.NetworkOutput
	}
	resp.TotalCPUPct = math.Round(resp.TotalCPUPct*100) / 100
	return resp
}

func collectSoftwareProcesses(inst softwareInstance) ([]softwareProcessInfo, error) {
	idsOut, err := runSoftwareCompose(inst, "ps", "-q")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(idsOut)
	if len(ids) == 0 {
		return []softwareProcessInfo{}, nil
	}
	meta := collectSoftwareContainerMeta(inst)
	out := []softwareProcessInfo{}
	for _, id := range ids {
		topOut, err := runSoftwareDocker("top", id)
		if err != nil {
			return out, fmt.Errorf("docker top %s failed: %w\n%s", id, err, topOut)
		}
		out = append(out, parseSoftwareTopOutput(id, meta[id], topOut)...)
	}
	return out, nil
}

func parseSoftwareTopOutput(containerID string, meta softwareContainerMeta, raw string) []softwareProcessInfo {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := []softwareProcessInfo{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		p := softwareProcessInfo{
			ContainerID:   containerID,
			ContainerName: meta.Name,
			Service:       meta.Service,
			User:          fields[0],
			PID:           fields[1],
		}
		if len(fields) > 2 {
			p.PPID = fields[2]
		}
		if len(fields) > 3 {
			p.CPU = fields[3]
		}
		if len(fields) > 4 {
			p.STime = fields[4]
		}
		if len(fields) > 5 {
			p.TTY = fields[5]
		}
		if len(fields) > 6 {
			p.Time = fields[6]
		}
		if len(fields) > 7 {
			p.Command = strings.Join(fields[7:], " ")
		} else if len(fields) > 2 {
			p.Command = strings.Join(fields[2:], " ")
		}
		out = append(out, p)
	}
	return out
}

type softwareContainerMeta struct {
	ID      string
	Name    string
	Service string
	State   string
}

func collectSoftwareContainerMeta(inst softwareInstance) map[string]softwareContainerMeta {
	out := map[string]softwareContainerMeta{}
	raw, err := runSoftwareCompose(inst, "ps", "--format", "json")
	if err != nil {
		return out
	}
	records := parseJSONRecords(raw)
	for _, rec := range records {
		name, _ := rec["Name"].(string)
		if name == "" {
			name, _ = rec["Names"].(string)
		}
		id, _ := rec["ID"].(string)
		if id == "" {
			id, _ = rec["Id"].(string)
		}
		service, _ := rec["Service"].(string)
		state, _ := rec["State"].(string)
		meta := softwareContainerMeta{ID: id, Name: name, Service: service, State: state}
		if name != "" {
			out[name] = meta
		}
		if id != "" {
			out[id] = meta
		}
	}
	return out
}

func parseJSONRecords(raw string) []map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return arr
	}
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func collectSoftwareVolumes(inst softwareInstance) []softwareVolumeInfo {
	keys := collectSoftwareVolumeKeys(inst)
	out := make([]softwareVolumeInfo, 0, len(keys))
	for _, key := range keys {
		name := softwareVolumeName(inst, key)
		info := softwareVolumeInfo{Name: name, Key: key, BackupHint: filepath.Join(softwareBackupRoot, inst.Name)}
		raw, err := runSoftwareDocker("volume", "inspect", name)
		if err == nil {
			var inspected []struct {
				Name       string `json:"Name"`
				Driver     string `json:"Driver"`
				Mountpoint string `json:"Mountpoint"`
				Scope      string `json:"Scope"`
			}
			if json.Unmarshal([]byte(raw), &inspected) == nil && len(inspected) > 0 {
				if inspected[0].Name != "" {
					info.Name = inspected[0].Name
				}
				info.Driver = inspected[0].Driver
				info.Mountpoint = inspected[0].Mountpoint
				info.Scope = inspected[0].Scope
			}
		}
		out = append(out, info)
	}
	return out
}

func collectSoftwareVolumeKeys(inst softwareInstance) []string {
	data, err := os.ReadFile(inst.ComposeFile)
	if err != nil {
		return nil
	}
	var doc struct {
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	keys := make([]string, 0, len(doc.Volumes))
	for key := range doc.Volumes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func listSoftwareBackups(inst softwareInstance) ([]softwareBackupInfo, error) {
	backupDir := filepath.Join(softwareBackupRoot, inst.Name)
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []softwareBackupInfo{}, nil
		}
		return nil, err
	}
	var out []softwareBackupInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, softwareBackupInfo{
			Name:      entry.Name(),
			Path:      filepath.Join(backupDir, entry.Name()),
			VolumeKey: softwareBackupVolumeKey(inst, entry.Name()),
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func resolveSoftwareBackupPath(inst softwareInstance, raw string) (string, string, error) {
	name := filepath.Base(strings.TrimSpace(raw))
	if name == "." || name == "" || !strings.HasSuffix(name, ".tar.gz") {
		return "", "", fmt.Errorf("invalid backup")
	}
	backupDir, err := filepath.Abs(filepath.Join(softwareBackupRoot, inst.Name))
	if err != nil {
		return "", "", err
	}
	full, err := filepath.Abs(filepath.Join(backupDir, name))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(backupDir, full)
	if err != nil {
		return "", "", err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("backup path escapes software backup directory")
	}
	volumeKey := softwareBackupVolumeKey(inst, name)
	if volumeKey == "" {
		return "", "", fmt.Errorf("backup filename does not match a known software volume")
	}
	return full, volumeKey, nil
}

func softwareBackupVolumeKey(inst softwareInstance, fileName string) string {
	prefix := sanitizeSoftwareName(inst.Name) + "-"
	suffix := ".tar.gz"
	if !strings.HasPrefix(fileName, prefix) || !strings.HasSuffix(fileName, suffix) {
		return ""
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(fileName, prefix), suffix)
	idx := strings.LastIndex(rest, "-")
	if idx <= 0 {
		return ""
	}
	candidate := rest[:idx]
	for _, key := range collectSoftwareVolumeKeys(inst) {
		if sanitizeSoftwareName(key) == candidate {
			return key
		}
	}
	return ""
}

func softwareVolumeName(inst softwareInstance, key string) string {
	return sanitizeSoftwareName(inst.Project) + "_" + sanitizeSoftwareName(key)
}

func parseDockerPercent(raw string) float64 {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "%"))
	if raw == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(raw, 64)
	return v
}

func parseDockerInt(raw string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(raw))
	return v
}

func parseDockerPair(raw string) (int64, int64) {
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return parseDockerBytes(raw), 0
	}
	return parseDockerBytes(parts[0]), parseDockerBytes(parts[1])
}

func parseDockerBytes(raw string) int64 {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, " ", ""))
	if raw == "" || raw == "--" {
		return 0
	}
	i := 0
	for i < len(raw) && (raw[i] == '.' || raw[i] == '-' || raw[i] == '+' || raw[i] >= '0' && raw[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0
	}
	num, _ := strconv.ParseFloat(raw[:i], 64)
	unit := strings.ToLower(raw[i:])
	mul := float64(1)
	switch unit {
	case "kb", "kib":
		mul = 1024
	case "mb", "mib":
		mul = 1024 * 1024
	case "gb", "gib":
		mul = 1024 * 1024 * 1024
	case "tb", "tib":
		mul = 1024 * 1024 * 1024 * 1024
	}
	return int64(num * mul)
}

func softwareComposeStatus(inst softwareInstance) string {
	out, err := runSoftwareCompose(inst, "ps", "-q")
	if err != nil {
		if isSoftwareComposeMissing(err) {
			return "needs-compose"
		}
		return "unknown"
	}
	if strings.TrimSpace(out) == "" {
		return "stopped"
	}
	return "running"
}
