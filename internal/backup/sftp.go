package backup

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SFTPProvider uploads backups to a remote server via SFTP/SCP over SSH.
// It uses golang.org/x/crypto/ssh (already in go.mod) with no additional
// dependencies. File operations are performed through the SSH subsystem
// "sftp" protocol, which is supported by all standard OpenSSH servers.
type SFTPProvider struct {
	host       string
	port       int
	user       string
	keyFile    string
	password   string
	remotePath string
}

// NewSFTPProvider creates an SFTPProvider.
func NewSFTPProvider(host string, port int, user, keyFile, password, remotePath string) *SFTPProvider {
	if port <= 0 {
		port = 22
	}
	if remotePath == "" {
		remotePath = "/backups/uwas"
	}
	return &SFTPProvider{
		host:       host,
		port:       port,
		user:       user,
		keyFile:    keyFile,
		password:   password,
		remotePath: remotePath,
	}
}

func (p *SFTPProvider) Name() string { return "sftp" }

func (p *SFTPProvider) Upload(ctx context.Context, filename string, data io.Reader) error {
	client, err := p.dial(ctx)
	if err != nil {
		return fmt.Errorf("sftp connect: %w", err)
	}
	defer client.Close()

	// Ensure remote directory exists.
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	session.Run("mkdir -p " + p.remotePath)
	session.Close()

	if err := safeBackupFilename(filename); err != nil {
		return err
	}
	// Use SCP-style upload via a shell command.
	remoteDest := path.Join(p.remotePath, filename)
	session, err = client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	// Read all data into memory so we know the size. For backups this is
	// typically small (config + certs).
	content, err := io.ReadAll(data)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Run("cat > " + remoteDest)
	}()

	if _, err := stdin.Write(content); err != nil {
		stdin.Close()
		return fmt.Errorf("write data: %w", err)
	}
	stdin.Close()

	return <-errCh
}

func (p *SFTPProvider) Download(ctx context.Context, filename string) (io.ReadCloser, error) {
	if err := safeBackupFilename(filename); err != nil {
		return nil, err
	}
	client, err := p.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("sftp connect: %w", err)
	}

	remoteSrc := path.Join(p.remotePath, filename)
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}

	if err := session.Start("cat " + remoteSrc); err != nil {
		session.Close()
		client.Close()
		return nil, err
	}

	return &sshReadCloser{Reader: stdout, session: session, client: client}, nil
}

func (p *SFTPProvider) List(ctx context.Context) ([]BackupInfo, error) {
	client, err := p.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("sftp connect: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	// List files with size and modification time.
	// Use stat-style output: filename size mtime(epoch)
	cmd := fmt.Sprintf(`find %s -maxdepth 1 -name '*.tar.gz' -printf '%%f\t%%s\t%%T@\n' 2>/dev/null || ls -1 %s/*.tar.gz 2>/dev/null`,
		p.remotePath, p.remotePath)
	out, err := session.Output(cmd)
	if err != nil {
		// Empty directory is not an error.
		return nil, nil
	}

	var infos []BackupInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) == 3 {
			// find -printf format: name\tsize\tepoch
			name := parts[0]
			size, _ := strconv.ParseInt(parts[1], 10, 64)
			epochF, _ := strconv.ParseFloat(parts[2], 64)
			t := time.Unix(int64(epochF), 0)
			infos = append(infos, BackupInfo{
				Name:     name,
				Size:     size,
				Created:  t,
				Provider: "sftp",
			})
		} else {
			// Fallback: just the filename from ls.
			name := path.Base(line)
			if strings.HasSuffix(name, ".tar.gz") {
				infos = append(infos, BackupInfo{
					Name:     name,
					Provider: "sftp",
				})
			}
		}
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Created.After(infos[j].Created)
	})
	return infos, nil
}

func (p *SFTPProvider) Delete(ctx context.Context, filename string) error {
	if err := safeBackupFilename(filename); err != nil {
		return err
	}
	client, err := p.dial(ctx)
	if err != nil {
		return fmt.Errorf("sftp connect: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	remotePath := path.Join(p.remotePath, filename)
	return session.Run("rm -f " + remotePath)
}

// --- SSH helpers ---

func (p *SFTPProvider) dial(ctx context.Context) (*ssh.Client, error) {
	var authMethods []ssh.AuthMethod

	// Key-based auth.
	if p.keyFile != "" {
		key, err := os.ReadFile(p.keyFile)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(key)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	// Password auth.
	if p.password != "" {
		authMethods = append(authMethods, ssh.Password(p.password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH auth method configured (set key_file or password)")
	}

	// Use known_hosts for TOFU (Trust On First Use) — validates against ~/.ssh/known_hosts
	// and auto-accepts new hosts on first connection.
	knownHostsFile := os.ExpandEnv("$HOME/.ssh/known_hosts")
	hostKeyCallback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		// File doesn't exist — create it empty so we can write new hosts
		f, err := os.OpenFile(knownHostsFile, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("create known_hosts: %w", err)
		}
		f.Close()
		hostKeyCallback, err = knownhosts.New(knownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("init knownhosts: %w", err)
		}
	}
	// Wrap knownhosts.New to auto-accept new hosts (TOFU) — append new keys to known_hosts
	trustedHostKeyCallback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := hostKeyCallback(hostname, remote, key)
		if err != nil {
			// Check if it's a known host with key changed (MITM) — reject for security
			if keyErr, ok := err.(*knownhosts.KeyError); ok && len(keyErr.Want) > 0 {
				return err // Reject MITM attempts
			}
			// Unknown host — accept and append to known_hosts (TOFU)
			hostEntry := fmt.Sprintf("%s %s %s", hostname, key.Type(), base64.StdEncoding.EncodeToString(key.Marshal()))
			f, fErr := os.OpenFile(knownHostsFile, os.O_APPEND|os.O_WRONLY, 0600)
			if fErr == nil {
				f.WriteString(string(hostEntry) + "\n")
				f.Close()
			}
			return nil
		}
		return nil
	}

	config := &ssh.ClientConfig{
		User:            p.user,
		Auth:            authMethods,
		HostKeyCallback: trustedHostKeyCallback,
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(p.host, strconv.Itoa(p.port))

	// Respect context cancellation via a net.Dialer.
	dialer := &net.Dialer{Timeout: config.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// sshReadCloser wraps an io.Reader from an SSH session so that Close() properly
// cleans up both the session and the SSH client connection.
type sshReadCloser struct {
	io.Reader
	session *ssh.Session
	client  *ssh.Client
}

func (r *sshReadCloser) Close() error {
	r.session.Close()
	return r.client.Close()
}

// safeBackupFilename rejects filenames that could inject shell metacharacters.
var safeBackupFilenameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func safeBackupFilename(name string) error {
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid backup filename")
	}
	if !safeBackupFilenameRe.MatchString(name) {
		return errors.New("backup filename contains unsafe characters")
	}
	return nil
}
