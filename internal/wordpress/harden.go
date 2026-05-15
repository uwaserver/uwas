// Security hardening for WordPress installations: WP_DEBUG / file
// edit / XML-RPC / SSL / auto-update / directory-listing / cron
// settings, applied via wp-config.php edits and helper files. Split
// out of installer.go per refactor.md A13.
package wordpress

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// --- Security Hardening ---

// SecurityStatus reports the current security state of a WordPress installation.
type SecurityStatus struct {
	XMLRPCDisabled     bool   `json:"xmlrpc_disabled"`
	FileEditDisabled   bool   `json:"file_edit_disabled"`
	DebugEnabled       bool   `json:"debug_enabled"`
	SSLForced          bool   `json:"ssl_forced"`
	AutoUpdatesCore    string `json:"auto_updates_core"` // "true", "false", "minor"
	AutoUpdatesPlugins bool   `json:"auto_updates_plugins"`
	AutoUpdatesThemes  bool   `json:"auto_updates_themes"`
	TablePrefix        string `json:"table_prefix"`
	PHPVersion         string `json:"php_version"`
	WPVersion          string `json:"wp_version"`
	DirectoryListing   bool   `json:"directory_listing_blocked"`
	WPCronDisabled     bool   `json:"wp_cron_disabled"`
}

// GetSecurityStatus checks the security configuration of a WordPress site.
func GetSecurityStatus(webRoot string) SecurityStatus {
	st := SecurityStatus{
		WPVersion: detectWPVersion(webRoot),
	}

	configPath := filepath.Join(webRoot, "wp-config.php")
	data, err := osReadFileFn(configPath)
	if err != nil {
		return st
	}
	content := string(data)

	st.FileEditDisabled = strings.Contains(content, "DISALLOW_FILE_EDIT") && containsDefineTrue(content, "DISALLOW_FILE_EDIT")
	st.DebugEnabled = containsDefineTrue(content, "WP_DEBUG")
	st.SSLForced = containsDefineTrue(content, "FORCE_SSL_ADMIN")
	st.WPCronDisabled = containsDefineTrue(content, "DISABLE_WP_CRON")

	// Auto-updates
	if containsDefineTrue(content, "WP_AUTO_UPDATE_CORE") {
		st.AutoUpdatesCore = "true"
	} else if strings.Contains(content, "'WP_AUTO_UPDATE_CORE'") && strings.Contains(content, "'minor'") {
		st.AutoUpdatesCore = "minor"
	} else {
		st.AutoUpdatesCore = "default"
	}
	st.AutoUpdatesPlugins = strings.Contains(content, "auto_update_plugin") && strings.Contains(content, "__return_true")
	st.AutoUpdatesThemes = strings.Contains(content, "auto_update_theme") && strings.Contains(content, "__return_true")

	// Table prefix
	if re := regexp.MustCompile(`\$table_prefix\s*=\s*'([^']+)'`); re.MatchString(content) {
		st.TablePrefix = re.FindStringSubmatch(content)[1]
	}

	// PHP version via wp-cli (extract just the version number, skip warnings)
	if phpOut, err := wpCLI(webRoot, "eval", "echo PHP_VERSION;"); err == nil {
		phpOut = strings.TrimSpace(phpOut)
		// Output may contain "Deprecated: ..." lines before the version
		lines := strings.Split(phpOut, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if len(line) > 0 && line[0] >= '0' && line[0] <= '9' {
				st.PHPVersion = line
				break
			}
		}
	}

	// XML-RPC — check if blocked via mu-plugin or wp-config
	st.XMLRPCDisabled = checkXMLRPCDisabled(webRoot, content)

	// Directory listing — check .htaccess for Options -Indexes
	if htData, err := osReadFileFn(filepath.Join(webRoot, ".htaccess")); err == nil {
		st.DirectoryListing = strings.Contains(string(htData), "-Indexes")
	}

	return st
}

func containsDefineTrue(content, constant string) bool {
	re := regexp.MustCompile(`define\s*\(\s*'` + constant + `'\s*,\s*true\s*\)`)
	return re.MatchString(content)
}

func checkXMLRPCDisabled(webRoot, wpConfig string) bool {
	// Check mu-plugin
	muPath := filepath.Join(webRoot, "wp-content", "mu-plugins", "uwas-security.php")
	if data, err := osReadFileFn(muPath); err == nil {
		if strings.Contains(string(data), "xmlrpc_enabled") {
			return true
		}
	}
	// Check wp-config for xmlrpc filter
	return strings.Contains(wpConfig, "xmlrpc_enabled")
}

// HardenOptions specifies which security features to enable/disable.
type HardenOptions struct {
	DisableXMLRPC   *bool `json:"disable_xmlrpc,omitempty"`
	DisableFileEdit *bool `json:"disable_file_edit,omitempty"`
	ForceSSLAdmin   *bool `json:"force_ssl_admin,omitempty"`
	DisableWPCron   *bool `json:"disable_wp_cron,omitempty"`
	BlockDirListing *bool `json:"block_dir_listing,omitempty"`
}

// Harden applies security hardening options to a WordPress installation.
func Harden(webRoot string, opts HardenOptions) (string, error) {
	var log strings.Builder

	// wp-config.php modifications
	configPath := filepath.Join(webRoot, "wp-config.php")
	data, err := osReadFileFn(configPath)
	if err != nil {
		return "", fmt.Errorf("read wp-config.php: %w", err)
	}
	content := string(data)
	changed := false

	if opts.DisableFileEdit != nil {
		content = setWPConfigDefine(content, "DISALLOW_FILE_EDIT", *opts.DisableFileEdit)
		if *opts.DisableFileEdit {
			log.WriteString("File editing disabled (DISALLOW_FILE_EDIT)\n")
		} else {
			log.WriteString("File editing enabled\n")
		}
		changed = true
	}

	if opts.ForceSSLAdmin != nil {
		content = setWPConfigDefine(content, "FORCE_SSL_ADMIN", *opts.ForceSSLAdmin)
		if *opts.ForceSSLAdmin {
			log.WriteString("SSL forced for admin (FORCE_SSL_ADMIN)\n")
		} else {
			log.WriteString("SSL admin enforcement removed\n")
		}
		changed = true
	}

	if opts.DisableWPCron != nil {
		content = setWPConfigDefine(content, "DISABLE_WP_CRON", *opts.DisableWPCron)
		if *opts.DisableWPCron {
			log.WriteString("WP-Cron disabled (use system cron instead)\n")
		} else {
			log.WriteString("WP-Cron enabled\n")
		}
		changed = true
	}

	if changed {
		if err := osWriteFileFn(configPath, []byte(content), 0600); err != nil {
			return log.String(), fmt.Errorf("write wp-config.php: %w", err)
		}
	}

	// XML-RPC via mu-plugin (cleaner than wp-config, survives updates)
	if opts.DisableXMLRPC != nil {
		muDir := filepath.Join(webRoot, "wp-content", "mu-plugins")
		osMkdirAllFn(muDir, 0755)
		muPath := filepath.Join(muDir, "uwas-security.php")

		if *opts.DisableXMLRPC {
			muContent := `<?php
// UWAS Security: Disable XML-RPC (prevents brute-force and DDoS attacks)
add_filter('xmlrpc_enabled', '__return_false');
add_filter('xmlrpc_methods', '__return_empty_array');
// Remove XML-RPC discovery link from head
remove_action('wp_head', 'rsd_link');
`
			if err := osWriteFileFn(muPath, []byte(muContent), 0644); err != nil {
				return log.String(), fmt.Errorf("write mu-plugin: %w", err)
			}
			log.WriteString("XML-RPC disabled via mu-plugin\n")
		} else {
			os.Remove(muPath)
			log.WriteString("XML-RPC enabled (mu-plugin removed)\n")
		}
	}

	// Directory listing in .htaccess
	if opts.BlockDirListing != nil {
		htPath := filepath.Join(webRoot, ".htaccess")
		htData, _ := osReadFileFn(htPath)
		htContent := string(htData)

		if *opts.BlockDirListing {
			if !strings.Contains(htContent, "-Indexes") {
				htContent = "Options -Indexes\n\n" + htContent
				osWriteFileFn(htPath, []byte(htContent), 0644)
				log.WriteString("Directory listing blocked (Options -Indexes)\n")
			}
		} else {
			htContent = strings.Replace(htContent, "Options -Indexes\n\n", "", 1)
			htContent = strings.Replace(htContent, "Options -Indexes\n", "", 1)
			osWriteFileFn(htPath, []byte(htContent), 0644)
			log.WriteString("Directory listing allowed\n")
		}
	}

	return log.String(), nil
}

func setWPConfigDefine(content, constant string, value bool) string {
	// Remove existing define for this constant
	re := regexp.MustCompile(`(?m)^\s*define\s*\(\s*'` + constant + `'\s*,\s*(?:true|false)\s*\)\s*;\s*\n?`)
	content = re.ReplaceAllString(content, "")

	// Insert new define before require_once ABSPATH
	val := "false"
	if value {
		val = "true"
	}
	define := fmt.Sprintf("define('%s', %s);\n", constant, val)

	if idx := strings.Index(content, "require_once ABSPATH"); idx >= 0 {
		content = content[:idx] + define + content[idx:]
	}
	return content
}
