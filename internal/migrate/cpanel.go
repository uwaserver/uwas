package migrate

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/domainutil"
)

// ErrInvalidArchive marks a backup the caller supplied that cannot be imported
// (not gzip, not a tar stream, over the extraction limits, no cPanel root), as
// opposed to a failure of the host. Match it with errors.Is; the message text
// is unchanged.
var ErrInvalidArchive = errors.New("invalid archive")

type archiveError struct{ msg string }

func (e *archiveError) Error() string        { return e.msg }
func (e *archiveError) Is(target error) bool { return target == ErrInvalidArchive }

func invalidArchivef(format string, args ...any) error {
	return &archiveError{msg: fmt.Sprintf(format, args...)}
}

// CPanelResult holds the result of a cPanel backup import.
type CPanelResult struct {
	User       string           `json:"user"`
	Domains    []CPanelDomain   `json:"domains"`
	Databases  []CPanelDatabase `json:"databases"`
	SSLCerts   int              `json:"ssl_certs"`
	FilesCount int              `json:"files_count"`
	Errors     []string         `json:"errors,omitempty"`
}

// CPanelDomain is a domain extracted from a cPanel backup.
type CPanelDomain struct {
	Domain     string `json:"domain"`
	DocRoot    string `json:"doc_root"`
	Type       string `json:"type"` // main, addon, sub
	SSL        bool   `json:"ssl"`
	PHPVersion string `json:"php_version,omitempty"`
	// Aliases are the parked domains and server aliases of the site, already
	// validated; names that are not hostnames are reported in Errors.
	Aliases []string `json:"aliases,omitempty"`
	// CertPath and KeyPath name the imported certificate pair when the backup
	// held a usable one; the caller wires them as manual TLS.
	CertPath string `json:"-"`
	KeyPath  string `json:"-"`
}

// CPanelDatabase is a database from a cPanel backup.
type CPanelDatabase struct {
	Name     string  `json:"name"`
	User     string  `json:"user"`
	SQLFile  string  `json:"sql_file"`
	SizeMB   float64 `json:"size_mb"`
	Imported bool    `json:"imported"`
}

// maxExtractFileBytes is the largest single file taken from a cPanel archive
// (a var so tests can lower it).
var maxExtractFileBytes int64 = 10 << 30

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
		return nil, invalidArchivef("gzip reader: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	// Temp dir for extraction
	extractDir, err := os.MkdirTemp("", "uwas-cpanel-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	// Aggregate caps (defense against decompression bombs): the per-file 10GB
	// limit below does not bound the total, so cap cumulative bytes and entry
	// count across the whole archive.
	const (
		maxTotalExtractBytes int64 = 50 << 30 // 50GB total
		maxExtractEntries          = 500_000  // entries
	)
	var totalBytes int64
	var entryCount int

	// Phase 1: Extract all files
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalidArchivef("read tar: %v", err)
		}

		entryCount++
		if entryCount > maxExtractEntries {
			return nil, invalidArchivef("archive exceeds entry-count limit of %d", maxExtractEntries)
		}

		// Security: prevent path traversal
		name := filepath.Clean(header.Name)
		// Block absolute paths (would bypass filepath.Join) and ".." segments
		// (would escape the extraction directory).
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			continue
		}

		target := filepath.Join(extractDir, name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				result.Errors = append(result.Errors, "mkdir "+name+": "+err.Error())
				continue
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				result.Errors = append(result.Errors, "mkdir "+name+": "+err.Error())
				continue
			}
			outFile, err := os.Create(target)
			if err != nil {
				result.Errors = append(result.Errors, "create "+name+": "+err.Error())
				continue
			}
			// Limit file size to 10GB. Read one byte more than the cap so a
			// file over it is recognised: stopping exactly at the cap looks
			// like a clean end of file and delivered the site a silently
			// truncated file.
			n, copyErr := io.Copy(outFile, io.LimitReader(tr, maxExtractFileBytes+1))
			outFile.Close()
			if copyErr != nil {
				result.Errors = append(result.Errors, "write "+name+": "+copyErr.Error())
				continue
			}
			if n > maxExtractFileBytes {
				os.Remove(target)
				result.Errors = append(result.Errors, fmt.Sprintf("skipped %s: larger than the %d byte per-file limit", name, maxExtractFileBytes))
				continue
			}
			totalBytes += n
			if totalBytes > maxTotalExtractBytes {
				return nil, invalidArchivef("archive exceeds aggregate extraction limit of %d bytes", maxTotalExtractBytes)
			}
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
			// A full-account backup's root is backup-<date>_<user>, so the
			// directory name is only a fallback for the account name.
			result.User = readCPanelUser(cpRoot, strings.TrimPrefix(e.Name(), "cpmove-"))
			break
		}
	}
	if cpRoot == "" {
		return nil, invalidArchivef("no cPanel root directory found in backup")
	}

	// Phase 3: Parse domain configuration from userdata/
	result.Domains = parseCPanelDomains(cpRoot)

	// Phase 4: Copy web files to target
	homeDir := filepath.Join(cpRoot, "homedir")
	if _, err := os.Stat(homeDir); err != nil {
		homeDir = cpRoot // fallback if no homedir subdir
	}

	tlsMaterial := loadCPanelTLSMaterial(cpRoot)
	imported := make([]string, 0, len(result.Domains))
	for _, d := range result.Domains {
		imported = append(imported, d.Domain)
	}
	kept := result.Domains[:0:0]
	for _, dom := range result.Domains {
		// dom.Domain and dom.DocRoot come from attacker-controlled userdata in
		// the uploaded archive. Reject a domain whose name isn't a safe single
		// path component (it's joined into targetDir and cert filenames), and
		// neutralize ".." in DocRoot via leading-slash Clean so the copy source
		// can't escape homeDir.
		if dom.Domain == "" || strings.ContainsAny(dom.Domain, `/\`) || strings.Contains(dom.Domain, "..") {
			result.Errors = append(result.Errors, "skipped domain with unsafe name: "+dom.Domain)
			continue
		}
		// The name also becomes a config host: canonicalise it the way config
		// matching does so a case variant of an existing tenant ("VICTIM.com")
		// can't slip past duplicate checks, and reject non-hostnames.
		if host := strings.ToLower(strings.TrimSuffix(dom.Domain, ".")); config.IsValidHostname(host) {
			dom.Domain = host
		} else {
			result.Errors = append(result.Errors, "skipped domain with invalid hostname: "+dom.Domain)
			continue
		}
		dom.Aliases = normalizeCPanelAliases(dom.Domain, dom.Aliases, imported, &result.Errors)
		docRoot := filepath.Clean("/" + dom.DocRoot)
		srcRoot := filepath.Join(homeDir, docRoot)
		if _, err := os.Stat(srcRoot); err != nil && dom.Type == "main" {
			// Only the main domain may fall back to public_html. Any other
			// domain whose document root is absent from the archive used to
			// receive a copy of the whole main site (wp-config.php, backups)
			// served under its own hostname.
			srcRoot = filepath.Join(homeDir, "public_html")
		}

		dstRoot := filepath.Join(targetDir, dom.Domain, "public_html")
		// Never copy over an existing site: the archive's files would replace
		// that tenant's live files. Same contract as Clone's non-empty target.
		if entries, err := os.ReadDir(dstRoot); err == nil && len(entries) > 0 {
			result.Errors = append(result.Errors, "skipped "+dom.Domain+": "+dstRoot+" already exists and is not empty")
			continue
		}
		if err := os.MkdirAll(dstRoot, 0755); err != nil {
			result.Errors = append(result.Errors, "mkdir "+dom.Domain+": "+err.Error())
		}

		if _, err := os.Stat(srcRoot); err == nil {
			if err := copyDir(srcRoot, dstRoot); err != nil {
				result.Errors = append(result.Errors, "copy "+dom.Domain+": "+err.Error())
			}
		} else {
			result.Errors = append(result.Errors, "no files for "+dom.Domain+": "+srcRoot)
		}

		// Certificate: matched by the names inside it, wherever pkgacct put it.
		// An expired certificate or one without its key is reported and the
		// domain keeps automatic TLS instead of being switched to a pair that
		// cannot be served.
		if certPEM, keyPEM, cerr := tlsMaterial.find(dom.Domain, time.Now()); cerr != nil {
			result.Errors = append(result.Errors, cerr.Error())
		} else if certPEM != nil {
			certPath, keyPath, werr := writeCPanelCertPair(filepath.Join(targetDir, ".certs", dom.Domain), certPEM, keyPEM)
			if werr != nil {
				result.Errors = append(result.Errors, "store certificate for "+dom.Domain+": "+werr.Error())
			} else {
				dom.SSL = true
				dom.CertPath, dom.KeyPath = certPath, keyPath
				result.SSLCerts++
			}
		}
		kept = append(kept, dom)
	}
	result.Domains = kept

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

	if importDB {
		importCPanelDatabases(result)
	}
	// The dumps live in the extraction directory removed on return; do not hand
	// the caller a path to a deleted file.
	for i := range result.Databases {
		result.Databases[i].SQLFile = ""
	}

	return result, nil
}

// importCPanelDatabases loads each discovered dump into a database of the same
// name. A database that already has tables is left alone (an import must not
// overwrite live data), names the SQL layer would not accept are reported
// rather than passed on, and dumps are streamed from disk, not read into
// memory. Database users and grants are not recreated: the backup carries only
// password hashes, so the operator creates the user in the Databases page.
func importCPanelDatabases(result *CPanelResult) {
	bin := ""
	for _, client := range []string{"mariadb", "mysql"} {
		if p, err := execLookPathFn(client); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		result.Errors = append(result.Errors, "databases not imported: neither the mariadb nor the mysql client was found")
		return
	}
	for i := range result.Databases {
		db := &result.Databases[i]
		if !validMigrateDBIdentifier(db.Name) {
			result.Errors = append(result.Errors, "database "+db.Name+" not imported: invalid database name")
			continue
		}
		probe := fmt.Sprintf("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='%s'", sqlString(db.Name))
		out, err := execCommandFn(bin, "-u", "root", "-N", "-B", "-e", probe).CombinedOutput()
		if err != nil {
			result.Errors = append(result.Errors, "database "+db.Name+" not imported: "+strings.TrimSpace(string(out)))
			continue
		}
		if strings.TrimSpace(string(out)) != "0" {
			result.Errors = append(result.Errors, "database "+db.Name+" not imported: it already has tables and is not overwritten")
			continue
		}
		create := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", sqlIdent(db.Name))
		if out, err := execCommandFn(bin, "-u", "root", "-e", create).CombinedOutput(); err != nil {
			result.Errors = append(result.Errors, "database "+db.Name+" not imported: "+strings.TrimSpace(string(out)))
			continue
		}
		dump, err := os.Open(db.SQLFile)
		if err != nil {
			result.Errors = append(result.Errors, "database "+db.Name+" not imported: "+err.Error())
			continue
		}
		cmd := execCommandFn(bin, "-u", "root", db.Name)
		cmd.Stdin = dump
		out, err = cmd.CombinedOutput()
		dump.Close()
		if err != nil {
			result.Errors = append(result.Errors, "database "+db.Name+" import failed: "+strings.TrimSpace(string(out)))
			continue
		}
		db.Imported = true
	}
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
	addons := readCPanelAddonDomains(filepath.Join(userdataDir, "main"))
	aliasOf := map[string][]string{}
	if mainDomain != "" {
		// Parked domains point at the main site.
		aliasOf[mainDomain] = append(aliasOf[mainDomain], readCPanelList(filepath.Join(userdataDir, "main"), "parked_domains")...)
	}

	for _, e := range entries {
		name := e.Name()
		// <vhost>.php-fpm.yaml (PHP-FPM accounts) and .cache files are
		// metadata beside the vhost files, not domains.
		if e.IsDir() || name == "main" || strings.HasSuffix(name, "_SSL") ||
			strings.HasSuffix(name, ".cache") || strings.HasSuffix(name, ".php-fpm.yaml") ||
			name == "." || name == ".." {
			continue
		}

		domain := name
		// An addon domain's vhost file is named after its subdomain form
		// (addon.example.com); the real domain is the key in addon_domains.
		realAddon, isAddon := addons[name]
		if isAddon {
			domain = realAddon
		}
		// serveralias names (also on the main domain's own vhost file, which
		// the seen check below skips) belong to the site the file describes.
		if sa := readCPanelServerAlias(filepath.Join(userdataDir, name)); len(sa) > 0 {
			aliasOf[domain] = append(aliasOf[domain], sa...)
		}
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
		if !isAddon && strings.Count(domain, ".") > 1 {
			dtype = "sub"
		}

		domains = append(domains, CPanelDomain{
			Domain:  domain,
			DocRoot: docRoot,
			Type:    dtype,
		})
	}

	for i := range domains {
		domains[i].Aliases = aliasOf[domains[i].Domain]
	}
	return domains
}

// readCPanelList reads a YAML block list ("key:" followed by "- value" lines).
func readCPanelList(path, key string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if !in {
			if strings.TrimSpace(line) == key+":" && (line == "" || line[0] != ' ') {
				in = true
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			if v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), `"'`); v != "" {
				out = append(out, v)
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		break // the next key ends the list
	}
	return out
}

// readCPanelServerAlias reads the space separated serveralias line of a vhost
// userdata file.
func readCPanelServerAlias(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m := regexp.MustCompile(`(?m)^serveralias:\s*(.+?)\s*$`).FindSubmatch(data)
	if len(m) < 2 {
		return nil
	}
	var out []string
	for _, f := range strings.Fields(strings.Trim(string(m[1]), `"'`)) {
		if f = strings.Trim(f, `"',`); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// normalizeCPanelAliases turns the raw parked-domain and serveralias names of a
// domain into config aliases: lower case, valid hostnames only, without the
// domain itself or its www form (apex and www are one site), without another
// imported domain (that is a site of its own), and without duplicates. A name
// that is not a hostname is reported.
func normalizeCPanelAliases(domain string, raw, imported []string, errs *[]string) []string {
	var out []string
	for _, a := range raw {
		a = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(a), "."))
		if a == "" {
			continue
		}
		if !config.IsValidHostname(a) {
			*errs = append(*errs, "skipped alias "+a+" of "+domain+": not a valid hostname")
			continue
		}
		if domainutil.SameHostname(a, domain) {
			continue
		}
		other := false
		for _, h := range imported {
			if domainutil.SameHostname(a, h) {
				other = true
				break
			}
		}
		if other {
			continue
		}
		dup := false
		for _, e := range out {
			if domainutil.SameHostname(a, e) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, a)
		}
	}
	return out
}

// readCPanelUser names the cPanel account of an extracted backup: cp_user in
// userdata/main, else the only file in cp/ (named after the account), else
// fallback.
func readCPanelUser(cpRoot, fallback string) string {
	valid := func(u string) bool {
		return u != "" && !strings.ContainsAny(u, "/\\ \t\r\n") && u != "." && u != ".."
	}
	if data, err := os.ReadFile(filepath.Join(cpRoot, "userdata", "main")); err == nil {
		if m := regexp.MustCompile(`(?m)^cp_user:\s*(\S+)\s*$`).FindSubmatch(data); len(m) > 1 {
			if u := strings.Trim(string(m[1]), `"'`); valid(u) {
				return u
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Join(cpRoot, "cp")); err == nil {
		var files []string
		for _, e := range entries {
			if !e.IsDir() {
				files = append(files, e.Name())
			}
		}
		if len(files) == 1 && valid(files[0]) {
			return files[0]
		}
	}
	return fallback
}

// readCPanelAddonDomains reads the addon_domains map of userdata/main
// ("  addon.test: addon.example.com") and returns it keyed by the vhost
// (subdomain) name that userdata/<vhost> is saved under, with the real addon
// domain as the value.
func readCPanelAddonDomains(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if !in {
			if strings.TrimSpace(line) == "addon_domains:" {
				in = true
			}
			continue
		}
		if line == "" || (line[0] != ' ' && line[0] != '\t') {
			break // the block ends at the next top-level key
		}
		key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key != "" && val != "" {
			out[val] = key
		}
	}
	return out
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

// homeDirName matches the first component of a cPanel home directory
// (/home, /home2, ...).
var homeDirName = regexp.MustCompile(`^home\d*$`)

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
		parts := strings.Split(docRoot, "/")
		// A path under the account's home directory (/home/<user>/..., also
		// /home2/<user>/...) is relative to that home; taking the last two
		// components turned /home/alice/addon.test into "alice/addon.test".
		if trimmed := strings.Split(strings.Trim(docRoot, "/"), "/"); len(trimmed) >= 3 && homeDirName.MatchString(trimmed[0]) {
			return strings.Join(trimmed[2:], "/")
		}
		// Return last 2 components
		if len(parts) >= 2 {
			return strings.Join(parts[len(parts)-2:], "/")
		}
		return filepath.Base(docRoot)
	}
	return ""
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

// copyPrivateFile copies src to dst with owner-only permissions, tightening an
// existing dst, so a private key never lands world-readable.
func copyPrivateFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := out.Chmod(0o600); err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return err
}
