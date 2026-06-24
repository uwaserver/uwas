package admin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func findSoftwareTemplate(id string) *softwareTemplate {
	id = strings.TrimSpace(id)
	for i := range softwareTemplates {
		if softwareTemplates[i].ID == id {
			return &softwareTemplates[i]
		}
	}
	return nil
}

func listSoftwareInstances() ([]softwareInstance, error) {
	entries, err := os.ReadDir(softwareLibraryRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []softwareInstance{}, nil
		}
		return nil, err
	}
	var out []softwareInstance
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		inst, err := loadSoftwareInstance(entry.Name())
		if err == nil {
			out = append(out, inst)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadSoftwareInstance(name string) (softwareInstance, error) {
	name = sanitizeSoftwareName(name)
	if name == "" {
		return softwareInstance{}, fmt.Errorf("invalid software name")
	}
	path := filepath.Join(softwareLibraryRoot, name, "uwas-software.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return softwareInstance{}, err
	}
	var inst softwareInstance
	if err := yaml.Unmarshal(data, &inst); err != nil {
		return softwareInstance{}, err
	}
	return inst, nil
}

func saveSoftwareInstance(inst softwareInstance) error {
	data, err := yaml.Marshal(&inst)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(inst.Dir, "uwas-software.yaml"), data, 0600)
}

func updateSoftwareComposeDomain(inst softwareInstance) error {
	if inst.TemplateID != "n8n" || strings.TrimSpace(inst.ComposeFile) == "" {
		return nil
	}
	data, err := os.ReadFile(inst.ComposeFile)
	if err != nil {
		return err
	}
	webhook := ""
	if inst.Domain != "" {
		webhook = "https://" + inst.Domain + "/"
	}
	updated, changed := replaceComposeEnvironmentLine(string(data), "N8N_HOST", inst.Domain)
	updated, changedWebhook := replaceComposeEnvironmentLine(updated, "WEBHOOK_URL", webhook)
	if !changed && !changedWebhook {
		return nil
	}
	return os.WriteFile(inst.ComposeFile, []byte(updated), 0600)
}

func replaceComposeEnvironmentLine(data, key, value string) (string, bool) {
	lines := strings.SplitAfter(data, "\n")
	changed := false
	prefix := "- " + key + "="
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		newline := ""
		if strings.HasSuffix(trimmed, "\r\n") {
			newline = "\r\n"
		} else if strings.HasSuffix(trimmed, "\n") {
			newline = "\n"
		}
		replacement := indent + prefix + value + newline
		if line != replacement {
			lines[i] = replacement
			changed = true
		}
	}
	return strings.Join(lines, ""), changed
}

func removeSoftwareInstanceDir(inst softwareInstance) error {
	root, err := filepath.Abs(softwareLibraryRoot)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(inst.Dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to remove path outside software library: %s", inst.Dir)
	}
	return os.RemoveAll(dir)
}

func sanitizeSoftwareName(name string) string {
	return strings.Trim(appNameLike(name), "-_")
}

func appNameLike(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == '.' || r == ' ' || r == '/':
			b.WriteByte('-')
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}

func fillSoftwareSecrets(templateID string, env map[string]string) {
	setDefault := func(k string) {
		if strings.TrimSpace(env[k]) == "" {
			env[k] = randomHex(16)
		}
	}
	switch templateID {
	case "adminer-postgres", "postgres":
		setDefault("POSTGRES_PASSWORD")
	case "mysql":
		setDefault("MYSQL_ROOT_PASSWORD")
		setDefault("MYSQL_PASSWORD")
	case "mariadb":
		setDefault("MARIADB_ROOT_PASSWORD")
		setDefault("MARIADB_PASSWORD")
	case "minio":
		if strings.TrimSpace(env["MINIO_ROOT_USER"]) == "" {
			env["MINIO_ROOT_USER"] = "admin"
		}
		setDefault("MINIO_ROOT_PASSWORD")
	case "n8n":
		setDefault("N8N_BASIC_AUTH_PASSWORD")
	}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "change-me-" + fmt.Sprint(n)
	}
	return hex.EncodeToString(buf)
}

func envValue(req softwareInstallRequest, key, fallback string) string {
	if v := strings.TrimSpace(req.Env[key]); v != "" {
		return v
	}
	return fallback
}
