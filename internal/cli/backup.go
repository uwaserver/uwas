package cli

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/backup"
)

// BackupCommand creates a backup archive of UWAS config and certs.
type BackupCommand struct{}

func (b *BackupCommand) Name() string        { return "backup" }
func (b *BackupCommand) Description() string { return "Backup config and certificates" }

func (b *BackupCommand) Help() string {
	return `Flags:
  --output string   Path for the backup archive (default "uwas-backup-<timestamp>.tar.gz")
  -c, --config string  Path to config file (default: found the way 'uwas serve' finds it)
  --certs string    Path to certificates directory (default "/var/lib/uwas/certs")

Examples:
  uwas backup --output /tmp/backup.tar.gz
  uwas backup -c /etc/uwas/uwas.yaml --output backup.tar.gz`
}

func (b *BackupCommand) Run(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	output := fs.String("output", "", "output path for backup archive")
	configPath := fs.String("c", "", "path to config file")
	fs.StringVar(configPath, "config", "", "path to config file (same as -c)")
	certsDir := fs.String("certs", "/var/lib/uwas/certs", "path to certificates directory")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Without -c, look where serve, config and the other commands look
	// (./uwas.yaml, ./.uwas, ~/.uwas, /etc/uwas). A fixed ./uwas.yaml made a
	// backup taken from any other directory of an installed server contain no
	// configuration at all (F52603).
	if *configPath == "" {
		if p, ok := findConfig(""); ok {
			*configPath = p
		} else {
			*configPath = "uwas.yaml"
		}
	}

	if *output == "" {
		*output = fmt.Sprintf("uwas-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	}

	n, err := createBackupCount(*output, *configPath, *certsDir)
	if err != nil {
		return err
	}
	// An archive with nothing in it is not a backup. It used to be reported as
	// "Backup created (0 files)" with exit code 0 when the config was not found
	// and the certificate directory was empty (F52603).
	if n == 0 {
		_ = os.Remove(*output)
		return fmt.Errorf("nothing to back up: no configuration at %s and no certificates in %s (use -c and --certs)", *configPath, *certsDir)
	}
	fmt.Printf("Backup created: %s (%d files)\n", *output, n)
	return nil
}

func createBackup(output, configPath, certsDir string) error {
	n, err := createBackupCount(output, configPath, certsDir)
	if err != nil {
		return err
	}
	fmt.Printf("Backup created: %s (%d files)\n", output, n)
	return nil
}

// createBackupCount writes the archive and returns how many files it holds.
func createBackupCount(output, configPath, certsDir string) (int, error) {
	// The archive bundles the config (API key, secrets) and TLS private keys,
	// so it must not be readable by other local users (os.Create gives 0644).
	outFile, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create backup file: %w", err)
	}
	defer outFile.Close()
	// Tighten a pre-existing wider file, but only a regular one: devices and
	// pipes (/dev/null, /dev/stdout) cannot or need not be chmod'ed, and a
	// filesystem that refuses chmod (vfat, cifs) must not make the backup fail.
	if st, err := outFile.Stat(); err == nil && st.Mode().IsRegular() {
		if err := outFile.Chmod(0o600); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not restrict %s to owner-only: %v\n", output, err)
		}
	}

	gw := gzip.NewWriter(outFile)
	tw := tar.NewWriter(gw)

	var fileCount int

	// Add main config file
	if err := addFileToTar(tw, configPath, "config/"+filepath.Base(configPath)); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("add config: %w", err)
		}
		if _, err := fmt.Fprintf(os.Stderr, "warning: config file not found: %s\n", configPath); err != nil {
			// stderr broken — nothing we can do
		}
	} else {
		fileCount++
	}

	// Add domains.d/ directory if it exists next to the config
	domainsDir := filepath.Join(filepath.Dir(configPath), "domains.d")
	if info, err := os.Stat(domainsDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(domainsDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
					continue
				}
				src := filepath.Join(domainsDir, name)
				if err := addFileToTar(tw, src, "config/domains.d/"+name); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not add %s: %v\n", src, err)
				} else {
					fileCount++
				}
			}
		}
	}

	// Add TLS certificates
	if info, err := os.Stat(certsDir); err == nil && info.IsDir() {
		err := filepath.Walk(certsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if info.IsDir() {
				return nil
			}
			relPath, _ := filepath.Rel(certsDir, path)
			tarPath := "certs/" + filepath.ToSlash(relPath)
			if err := addFileToTar(tw, path, tarPath); err != nil {
				fmt.Fprintln(os.Stderr, "warning: could not add", path, ":", err)
			} else {
				fileCount++
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: error walking certs dir: %v\n", err)
		}
	}

	if err := tw.Close(); err != nil {
		gw.Close()
		return 0, fmt.Errorf("finalize tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return 0, fmt.Errorf("finalize gzip: %w", err)
	}

	return fileCount, nil
}

func addFileToTar(tw *tar.Writer, srcPath, tarName string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", srcPath, err)
	}
	// Decide before the header goes out: a directory (a symlink to one is
	// not walked into) or other special file would announce its stat size
	// and then supply no data, leaving the archive unable to be finished.
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", srcPath)
	}

	header := &tar.Header{
		Name:    tarName,
		Size:    info.Size(),
		Mode:    int64(info.Mode()),
		ModTime: info.ModTime(),
	}

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header for %s: %w", tarName, err)
	}

	if _, err := io.Copy(tw, file); err != nil {
		return fmt.Errorf("copy %s to tar: %w", srcPath, err)
	}
	return nil
}

// RestoreCommand restores from a backup archive.
type RestoreCommand struct{}

func (r *RestoreCommand) Name() string        { return "restore" }
func (r *RestoreCommand) Description() string { return "Restore config and certificates from backup" }

func (r *RestoreCommand) Help() string {
	return `Flags:
  --input string    Path to the backup archive (required)
  --config-dir string  Destination for config files (default "/etc/uwas")
  --certs-dir string   Destination for certificates (default "/var/lib/uwas/certs")

Examples:
  uwas restore --input /tmp/backup.tar.gz
  uwas restore --input backup.tar.gz --config-dir /etc/uwas --certs-dir /var/lib/uwas/certs`
}

func (r *RestoreCommand) Run(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	input := fs.String("input", "", "path to backup archive")
	configDir := fs.String("config-dir", "/etc/uwas", "destination for config files")
	certsDir := fs.String("certs-dir", "/var/lib/uwas/certs", "destination for certificates")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *input == "" {
		return fmt.Errorf("--input is required")
	}

	return restoreBackup(*input, *configDir, *certsDir)
}

func restoreBackup(input, configDir, certsDir string) error {
	file, err := os.Open(input)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()

	gr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("decompress backup: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	// Entries are staged next to their destination and only renamed into place
	// once the whole archive has been read: a truncated or corrupt backup must
	// not leave the live config or a TLS key half-overwritten (F8010).
	type staged struct{ tmp, dest string }
	var pending []staged
	cleanup := func() {
		for _, st := range pending {
			os.Remove(st.tmp)
		}
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return fmt.Errorf("read tar: %w", err)
		}
		// Only regular files are restored; directory, link and device entries
		// would otherwise be created as (truncated) plain files.
		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Determine destination based on archive path prefix
		var destPath string
		var baseDir string
		switch {
		case strings.HasPrefix(header.Name, "config/"):
			relPath := strings.TrimPrefix(header.Name, "config/")
			destPath = filepath.Join(configDir, relPath)
			baseDir = configDir
		case strings.HasPrefix(header.Name, "certs/"):
			relPath := strings.TrimPrefix(header.Name, "certs/")
			destPath = filepath.Join(certsDir, relPath)
			baseDir = certsDir
		default:
			// Skip unknown entries
			continue
		}

		// Validate path to prevent traversal
		destPath = filepath.Clean(destPath)
		if !backup.IsInsideDir(destPath, baseDir) {
			fmt.Fprintf(os.Stderr, "warning: skipping traversal path: %s\n", header.Name)
			continue
		}

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			cleanup()
			return fmt.Errorf("create dir for %s: %w", destPath, err)
		}

		// Write the entry to a sibling temp file (exclusive create, so a
		// planted link at the temp name is never followed).
		tmpPath := destPath + ".uwas-restore"
		os.Remove(tmpPath)
		// Replacing a file keeps its permissions (a hardened 0600 config or key
		// must not become whatever the archive recorded); a new file takes the
		// archive's mode minus anything beyond rwxr-xr-x (F8300).
		mode := os.FileMode(header.Mode) & 0o755
		if info, lerr := os.Lstat(destPath); lerr == nil && info.Mode().IsRegular() {
			mode = info.Mode().Perm()
		}
		outFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			cleanup()
			return fmt.Errorf("create %s: %w", destPath, err)
		}
		if err := outFile.Chmod(mode); err != nil {
			outFile.Close()
			os.Remove(tmpPath)
			cleanup()
			return fmt.Errorf("chmod %s: %w", destPath, err)
		}
		// A member named twice (tar --append) shares this temp file, which was
		// just recreated above: keep only the last copy, as tar itself does,
		// or the install loop would rename the same temp twice and drop every
		// member after it.
		kept := pending[:0]
		for _, st := range pending {
			if st.dest != destPath {
				kept = append(kept, st)
			}
		}
		pending = append(kept, staged{tmpPath, destPath})

		// Limit copy to prevent decompression bombs
		if _, err := io.Copy(outFile, io.LimitReader(tr, header.Size)); err != nil {
			outFile.Close()
			cleanup()
			return fmt.Errorf("write %s: %w", destPath, err)
		}
		if err := outFile.Close(); err != nil {
			cleanup()
			return fmt.Errorf("write %s: %w", destPath, err)
		}
	}

	// tar stops at its end-of-archive marker; drain the rest so gzip verifies
	// its trailer (CRC and length) before anything is installed.
	if _, err := io.Copy(io.Discard, gr); err != nil {
		cleanup()
		return fmt.Errorf("verify backup: %w", err)
	}

	fileCount := 0
	for i, st := range pending {
		if err := os.Rename(st.tmp, st.dest); err != nil {
			for _, rest := range pending[i:] {
				os.Remove(rest.tmp)
			}
			return fmt.Errorf("install %s: %w", st.dest, err)
		}
		fileCount++
	}

	fmt.Printf("Restore complete: %d files restored\n", fileCount)
	fmt.Println("Run 'uwas reload' to apply the restored configuration.")
	return nil
}
