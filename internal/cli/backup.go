package cli

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupCommand creates a backup archive of UWAS config and certs.
type BackupCommand struct{}

func (b *BackupCommand) Name() string        { return "backup" }
func (b *BackupCommand) Description() string { return "Backup config and certificates" }

func (b *BackupCommand) Help() string {
	return `Flags:
  --output string   Path for the backup archive (default "uwas-backup-<timestamp>.tar.gz")
  -c string         Path to config file (default "uwas.yaml")
  --certs string    Path to certificates directory (default "/var/lib/uwas/certs")

Examples:
  uwas backup --output /tmp/backup.tar.gz
  uwas backup -c /etc/uwas/uwas.yaml --output backup.tar.gz`
}

func (b *BackupCommand) Run(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	output := fs.String("output", "", "output path for backup archive")
	configPath := fs.String("c", "uwas.yaml", "path to config file")
	certsDir := fs.String("certs", "/var/lib/uwas/certs", "path to certificates directory")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *output == "" {
		*output = fmt.Sprintf("uwas-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	}

	return createBackup(*output, *configPath, *certsDir)
}

func createBackup(output, configPath, certsDir string) error {
	outFile, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	var fileCount int

	// Add main config file
	if err := addFileToTar(tw, configPath, "config/"+filepath.Base(configPath)); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("add config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "warning: config file not found: %s\n", configPath)
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
				fmt.Fprintf(os.Stderr, "warning: could not add %s: %v\n", path, err)
			} else {
				fileCount++
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: error walking certs dir: %v\n", err)
		}
	}

	fmt.Printf("Backup created: %s (%d files)\n", output, fileCount)
	return nil
}

func addFileToTar(tw *tar.Writer, srcPath, tarName string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name:    tarName,
		Size:    info.Size(),
		Mode:    int64(info.Mode()),
		ModTime: info.ModTime(),
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	_, err = io.Copy(tw, file)
	return err
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

	var fileCount int

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		// Determine destination based on archive path prefix
		var destPath string
		switch {
		case strings.HasPrefix(header.Name, "config/"):
			relPath := strings.TrimPrefix(header.Name, "config/")
			destPath = filepath.Join(configDir, relPath)
		case strings.HasPrefix(header.Name, "certs/"):
			relPath := strings.TrimPrefix(header.Name, "certs/")
			destPath = filepath.Join(certsDir, relPath)
		default:
			// Skip unknown entries
			continue
		}

		// Validate path to prevent traversal
		destPath = filepath.Clean(destPath)
		if strings.Contains(destPath, "..") {
			fmt.Fprintf(os.Stderr, "warning: skipping suspicious path: %s\n", header.Name)
			continue
		}

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("create dir for %s: %w", destPath, err)
		}

		// Write file
		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
		if err != nil {
			return fmt.Errorf("create %s: %w", destPath, err)
		}

		// Limit copy to prevent decompression bombs
		if _, err := io.Copy(outFile, io.LimitReader(tr, header.Size)); err != nil {
			outFile.Close()
			return fmt.Errorf("write %s: %w", destPath, err)
		}
		outFile.Close()
		fileCount++
	}

	fmt.Printf("Restore complete: %d files restored\n", fileCount)
	fmt.Println("Run 'uwas reload' to apply the restored configuration.")
	return nil
}
