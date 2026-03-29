package migrate

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CPanelResult holds the result of a cPanel backup import.
type CPanelResult struct {
	User        string           `json:"user"`
	Domains     []CPanelDomain   `json:"domains"`
	Databases   []CPanelDatabase `json:"databases"`
	SSLCerts    int              `json:"ssl_certs"`
	FilesCount  int              `json:"files_count"`
	Errors      []string         `json:"errors,omitempty"`
}

// CPanelDomain is a domain extracted from a cPanel backup.
type CPanelDomain struct {
	Domain    string `json:"domain"`
	DocRoot   string `json:"doc_root"`
	Type      string `json:"type"` // main, addon, sub
	SSL       bool   `json:"ssl"`
	PHPVersion string `json:"php_version,omitempty"`
}

// CPanelDatabase is a database from a cPanel backup.
type CPanelDatabase struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	SQLFile  string `json:"sql_file"`
	SizeMB   float64 `json:"size_mb"`
	Imported bool   `json:"imported"`
}

// ImportCPanelBackup extracts a cPanel backup (cpmove-*.tar.gz) into UWAS format.
// It extracts web files, discovers domains, and optionally imports databases.
func ImportCPanelBackup(backupPath, targetDir string, importDB bool) (*CPanelResult, error) {
	result := &CPanelResult{}

	f, err := os.Open(backupPath)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	// Temp dir for extraction
	extractDir, err := os.MkdirTemp("", "uwas-cpanel-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	// Phase 1: Extract all files
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		// Security: prevent path traversal
		name := filepath.Clean(header.Name)
		if strings.Contains(name, "..") {
			continue
		}

		target := filepath.Join(extractDir, name)

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			outFile, err := os.Create(target)
			if err != nil {
				result.Errors = append(result.Errors, "create "+name+": "+err.Error())
				continue
			}
			// Limit file size to 10GB
			if _, err := io.Copy(outFile, io.LimitReader(tr, 10<<30)); err != nil {
				outFile.Close()
				result.Errors = append(result.Errors, "write "+name+": "+err.Error())
				continue
			}
			outFile.Close()
			result.FilesCount++
		}
	}

	// Phase 2: Detect cPanel user from directory structure
	// cpmove archives have: cpmove-{user}/homedir/ or just {user}/homedir/
	entries, _ := os.ReadDir(extractDir)
	var cpRoot string
	for _, e := range entries {
		if e.IsDir() {
			cpRoot = filepath.Join(extractDir, e.Name())
			// Extract user from dir name
			name := e.Name()
			result.User = strings.TrimPrefix(name, "cpmove-")
			break
		}
	}
	if cpRoot == "" {
		return nil, fmt.Errorf("no cPanel root directory found in backup")
	}

	// Phase 3: Parse domain configuration from userdata/
	result.Domains = parseCPanelDomains(cpRoot)

	// Phase 4: Copy web files to target
	homeDir := filepath.Join(cpRoot, "homedir")
	if _, err := os.Stat(homeDir); err != nil {
		homeDir = cpRoot // fallback if no homedir subdir
	}

	for i, dom := range result.Domains {
		srcRoot := filepath.Join(homeDir, dom.DocRoot)
		if _, err := os.Stat(srcRoot); err != nil {
			// Try relative to homedir
			srcRoot = filepath.Join(homeDir, "public_html")
			if dom.Type == "addon" {
				srcRoot = filepath.Join(homeDir, dom.DocRoot)
			}
		}

		dstRoot := filepath.Join(targetDir, dom.Domain, "public_html")
		os.MkdirAll(dstRoot, 0755)

		if _, err := os.Stat(srcRoot); err == nil {
			if err := copyDir(srcRoot, dstRoot); err != nil {
				result.Errors = append(result.Errors, "copy "+dom.Domain+": "+err.Error())
			}
		} else {
			result.Errors = append(result.Errors, "no files for "+dom.Domain+": "+srcRoot)
		}

		// Check for SSL
		sslDir := filepath.Join(cpRoot, "ssl")
		certFile := filepath.Join(sslDir, dom.Domain+".crt")
		if _, err := os.Stat(certFile); err == nil {
			result.Domains[i].SSL = true
			result.SSLCerts++
			// Copy SSL cert and key to UWAS cert dir
			certDst := filepath.Join(targetDir, ".certs", dom.Domain)
			os.MkdirAll(certDst, 0700)
			copyFile(certFile, filepath.Join(certDst, "cert.pem"))
			keyFile := filepath.Join(sslDir, dom.Domain+".key")
			if _, err := os.Stat(keyFile); err == nil {
				copyFile(keyFile, filepath.Join(certDst, "key.pem"))
			}
		}
	}

	// Phase 5: Discover and optionally import databases
	mysqlDir := filepath.Join(cpRoot, "mysql")
	if entries, err := os.ReadDir(mysqlDir); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			info, _ := e.Info()
			sizeMB := float64(0)
			if info != nil {
				sizeMB = float64(info.Size()) / (1024 * 1024)
			}
			db := CPanelDatabase{
				Name:    strings.TrimSuffix(e.Name(), ".sql"),
				User:    result.User,
				SQLFile: filepath.Join(mysqlDir, e.Name()),
				SizeMB:  sizeMB,
			}
			result.Databases = append(result.Databases, db)
		}
	}

	return result, nil
}

// parseCPanelDomains reads domain info from cPanel userdata directory.
func parseCPanelDomains(cpRoot string) []CPanelDomain {
	var domains []CPanelDomain

	userdataDir := filepath.Join(cpRoot, "userdata")

	// Main domain: userdata/main
	mainDomain := readCPanelUserdata(filepath.Join(userdataDir, "main"))
	if mainDomain != "" {
		domains = append(domains, CPanelDomain{
			Domain:  mainDomain,
			DocRoot: "public_html",
			Type:    "main",
		})
	}

	// Read all domain files from userdata/
	entries, err := os.ReadDir(userdataDir)
	if err != nil {
		// No userdata — try to detect from directory structure
		if _, err := os.Stat(filepath.Join(cpRoot, "homedir", "public_html")); err == nil {
			if len(domains) == 0 {
				domains = append(domains, CPanelDomain{
					Domain:  "unknown",
					DocRoot: "public_html",
					Type:    "main",
				})
			}
		}
		return domains
	}

	seen := map[string]bool{}
	if mainDomain != "" {
		seen[mainDomain] = true
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == "main" || strings.HasSuffix(name, "_SSL") ||
			strings.HasSuffix(name, ".cache") || name == "." || name == ".." {
			continue
		}

		domain := name
		if seen[domain] {
			continue
		}
		seen[domain] = true

		// Read docroot from userdata file
		docRoot := readCPanelDocRoot(filepath.Join(userdataDir, name))
		if docRoot == "" {
			docRoot = "public_html/" + domain
		}

		dtype := "addon"
		if strings.Count(domain, ".") > 1 {
			dtype = "sub"
		}

		domains = append(domains, CPanelDomain{
			Domain:  domain,
			DocRoot: docRoot,
			Type:    dtype,
		})
	}

	return domains
}

// readCPanelUserdata reads the main domain name from a cPanel userdata file.
func readCPanelUserdata(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Format: "main_domain: example.com"
	re := regexp.MustCompile(`(?m)^main_domain:\s*(.+)$`)
	if m := re.FindSubmatch(data); len(m) > 1 {
		return strings.TrimSpace(string(m[1]))
	}
	// Simple key=value format
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// readCPanelDocRoot reads the document root from a cPanel userdata domain file.
func readCPanelDocRoot(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?m)^documentroot:\s*(.+)$`)
	if m := re.FindSubmatch(data); len(m) > 1 {
		docRoot := strings.TrimSpace(string(m[1]))
		// Convert absolute path to relative (strip /home/user/)
		if idx := strings.Index(docRoot, "/public_html"); idx >= 0 {
			return docRoot[idx+1:]
		}
		// Return last 2 components
		parts := strings.Split(docRoot, "/")
		if len(parts) >= 2 {
			return strings.Join(parts[len(parts)-2:], "/")
		}
		return filepath.Base(docRoot)
	}
	return ""
}

// GenerateUWASConfig creates UWAS domain YAML configs from cPanel import results.
func GenerateUWASConfig(result *CPanelResult, webRoot string) []map[string]any {
	var configs []map[string]any
	for _, dom := range result.Domains {
		cfg := map[string]any{
			"host": dom.Domain,
			"type": "php",
			"root": filepath.Join(webRoot, dom.Domain, "public_html"),
			"ssl":  map[string]string{"mode": "auto"},
		}
		if dom.SSL {
			cfg["ssl"] = map[string]string{"mode": "manual"}
		}
		configs = append(configs, cfg)
	}
	return configs
}

// ExportCPanelResultJSON returns JSON summary for API response.
func ExportCPanelResultJSON(result *CPanelResult) ([]byte, error) {
	return json.MarshalIndent(result, "", "  ")
}

// copyDir recursively copies a directory.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
