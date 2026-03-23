package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// uwasDir returns the default UWAS config directory.
// On Windows: %USERPROFILE%\.uwas
// On Linux/Mac: ~/.uwas
func uwasDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".uwas")
}

// defaultConfigPath returns the path to the default config file.
func defaultConfigPath() string {
	return filepath.Join(uwasDir(), "uwas.yaml")
}

// findConfig searches for a config file in common locations.
// Returns the path and whether it was found.
func findConfig(explicit string) (string, bool) {
	// Explicit path always wins
	if explicit != "" && explicit != "uwas.yaml" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit, true
		}
		return explicit, false
	}

	// Search order:
	// 1. ./uwas.yaml (current directory)
	// 2. ./uwas.yml
	// 3. ./.uwas/uwas.yaml
	// 4. ~/.uwas/uwas.yaml
	// 5. /etc/uwas/uwas.yaml (Linux only)
	candidates := []string{
		"uwas.yaml",
		"uwas.yml",
		filepath.Join(".uwas", "uwas.yaml"),
		defaultConfigPath(),
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "/etc/uwas/uwas.yaml")
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}

	return defaultConfigPath(), false
}

// generateAPIKey creates a random 32-char hex API key.
func generateAPIKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ensureDefaultConfig creates the default config directory and file if they
// don't exist. Returns the config path.
func ensureDefaultConfig(httpPort, adminPort, adminBind string) (string, error) {
	dir := uwasDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}

	// Create subdirectories
	for _, sub := range []string{"domains.d", "certs", "cache", "logs", "backups"} {
		os.MkdirAll(filepath.Join(dir, sub), 0755)
	}

	// Create www directory with a default page
	wwwDir := filepath.Join(dir, "www")
	os.MkdirAll(wwwDir, 0755)
	indexPath := filepath.Join(wwwDir, "index.html")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		os.WriteFile(indexPath, []byte(defaultIndexHTML), 0644)
	}

	cfgPath := filepath.Join(dir, "uwas.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return cfgPath, nil // already exists
	}

	// Generate config with provided ports
	apiKey := generateAPIKey()
	configContent := generateDefaultConfig(httpPort, adminPort, adminBind, apiKey, dir)

	if err := os.WriteFile(cfgPath, []byte(configContent), 0644); err != nil {
		return "", fmt.Errorf("write default config: %w", err)
	}

	// Write .env file
	envPath := filepath.Join(dir, ".env")
	envContent := fmt.Sprintf("UWAS_ADMIN_KEY=%s\nUWAS_PURGE_KEY=%s\n", apiKey, generateAPIKey())
	os.WriteFile(envPath, []byte(envContent), 0644)

	fmt.Printf("\n  %s Created default configuration\n", colorize("*", "green"))
	fmt.Printf("    Config:    %s\n", cfgPath)
	fmt.Printf("    API Key:   %s\n", apiKey)
	fmt.Printf("    Web Root:  %s\n", wwwDir)
	dashHost := adminBind
	if dashHost == "0.0.0.0" {
		dashHost = "127.0.0.1"
	}
	fmt.Printf("    Dashboard: http://%s:%s/_uwas/dashboard/\n\n", dashHost, adminPort)

	return cfgPath, nil
}

func generateDefaultConfig(httpPort, adminPort, adminBind, apiKey, baseDir string) string {
	// Normalize paths for the config (use forward slashes)
	wwwDir := filepath.ToSlash(filepath.Join(baseDir, "www"))
	certsDir := filepath.ToSlash(filepath.Join(baseDir, "certs"))
	cacheDir := filepath.ToSlash(filepath.Join(baseDir, "cache"))
	backupsDir := filepath.ToSlash(filepath.Join(baseDir, "backups"))

	listenAddr := ":" + httpPort
	adminAddr := adminBind + ":" + adminPort

	return strings.TrimSpace(fmt.Sprintf(`# UWAS — Unified Web Application Server
# Auto-generated configuration — edit as needed
# Docs: https://github.com/uwaserver/uwas

global:
  worker_count: auto
  max_connections: 65536
  http_listen: "%s"
  log_level: info
  log_format: text

  timeouts:
    read: 30s
    write: 60s
    idle: 120s
    shutdown_grace: 10s

  admin:
    enabled: true
    listen: "%s"
    api_key: "%s"

  acme:
    email: ""
    storage: %s

  cache:
    enabled: true
    memory_limit: 256MB
    disk_path: %s
    default_ttl: 3600

  backup:
    enabled: true
    provider: local
    keep: 7
    local:
      path: %s

domains:
  - host: "localhost:%s"
    aliases:
      - "127.0.0.1:%s"
    root: %s
    type: static
    ssl:
      mode: "off"
    compression:
      enabled: true
      algorithms:
        - gzip
        - br
    cache:
      enabled: true
      ttl: 300
    security:
      blocked_paths:
        - ".git"
        - ".env"
`, listenAddr, adminAddr, apiKey,
		certsDir, cacheDir, backupsDir,
		httpPort, httpPort, wwwDir)) + "\n"
}

const defaultIndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>UWAS</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif; background: #0f172a; color: #e2e8f0; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
    .c { text-align: center; max-width: 500px; padding: 2rem; }
    h1 { font-size: 3rem; margin-bottom: 0.5rem; background: linear-gradient(135deg, #3b82f6, #8b5cf6); -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text; }
    p { color: #94a3b8; margin-bottom: 1.5rem; line-height: 1.6; }
    .b { display: inline-block; background: #1e293b; border: 1px solid #334155; border-radius: 9999px; padding: 0.25rem 0.75rem; font-size: 0.75rem; color: #10b981; margin: 0.15rem; }
    a { color: #3b82f6; text-decoration: none; } a:hover { text-decoration: underline; }
    .f { margin-top: 2rem; font-size: 0.8rem; color: #475569; }
  </style>
</head>
<body>
  <div class="c">
    <h1>UWAS</h1>
    <p>Your server is running. Edit the config to add your domains.</p>
    <div>
      <span class="b">HTTP/2</span>
      <span class="b">Auto HTTPS</span>
      <span class="b">PHP</span>
      <span class="b">Reverse Proxy</span>
      <span class="b">Cache</span>
      <span class="b">WAF</span>
    </div>
    <p class="f">
      <a href="/_uwas/dashboard/">Open Dashboard</a>
    </p>
  </div>
</body>
</html>
`
