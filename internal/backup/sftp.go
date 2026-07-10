package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
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
	host               string
	port               int
	user               string
	keyFile            string
	password           string
	remotePath         string
	insecureKnownHosts bool // Allow unknown hosts (auto-accept TOFU — not recommended)
}

// NewSFTPProvider creates an SFTPProvider.
func NewSFTPProvider(host string, port int, user, keyFile, password, remotePath string, insecureKnownHosts ...bool) *SFTPProvider {
	if port <= 0 {
		port = 22
	}
	if remotePath == "" {
		remotePath = "/backups/uwas"
	}
	insecure := false
	if len(insecureKnownHosts) > 0 {
		insecure = insecureKnownHosts[0]
	}
	return &SFTPProvider{
		host:               host,
		port:               port,
		user:               user,
		keyFile:            keyFile,
		password:           password,
		remotePath:         remotePath,
		insecureKnownHosts: insecure,
	}
}

// knownHostsPathOverride, when non-empty, replaces the default
// ~/.ssh/known_hosts path used for SSH host-key verification. Tests set it to an
// isolated temp file so they neither read from nor pollute the real user
// known_hosts (which previously caused intermittent "key mismatch" failures
// when a dynamic test port was reused across runs with a fresh host key).
var knownHostsPathOverride string

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
	mkdirErr := session.Run("mkdir -p -- " + shellQuote(p.remotePath))
	session.Close()
	if mkdirErr != nil {
		return fmt.Errorf("sftp mkdir %q: %w", p.remotePath, mkdirErr)
	}

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

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Run("cat > " + shellQuote(remoteDest))
	}()

	// Stream the archive straight to the remote `cat` instead of buffering it
	// in memory. Backups (esp. full domain archives) can be arbitrarily large;
	// the previous 100MB in-memory cap hard-failed those. io.Copy reports a
	// short write as an error, preserving the same failure semantics.
	writeErr := func() error {
		_, err := io.Copy(stdin, data)
		return err
	}()
	stdin.Close()

	// Always wait for the remote `cat` to finish. On a write failure the remote
	// error (e.g. disk full / permission denied) is usually the real cause, so
	// prefer it over the local pipe error.
	runErr := <-errCh
	if runErr != nil {
		return fmt.Errorf("sftp upload %q: %w", remoteDest, runErr)
	}
	if writeErr != nil {
		return fmt.Errorf("write data: %w", writeErr)
	}
	return nil
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

	if err := session.Start("cat -- " + shellQuote(remoteSrc)); err != nil {
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
	quotedRemotePath := shellQuote(p.remotePath)
	cmd := fmt.Sprintf(`find %s -maxdepth 1 -name '*.tar.gz' -printf '%%f\t%%s\t%%T@\n' 2>/dev/null || ls -1 %s/*.tar.gz 2>/dev/null`,
		quotedRemotePath, quotedRemotePath)
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
	return session.Run("rm -f -- " + shellQuote(remotePath))
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

	// Determine the host-key verification policy.
	var hostKeyCallback ssh.HostKeyCallback
	if p.insecureKnownHosts {
		// Explicit dev/test opt-out: accept any host key without consulting or
		// writing known_hosts. (Persisting + re-validating would otherwise
		// reject a host whose key changed — e.g. a reused dynamic test port —
		// which contradicts the documented "auto-accept" intent.)
		hostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec // gated behind explicit opt-in
	} else {
		// Use known_hosts for TOFU (Trust On First Use) — validates against
		// ~/.ssh/known_hosts and rejects unknown hosts and changed keys.
		knownHostsFile := knownHostsPathOverride
		if knownHostsFile == "" {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				home = os.Getenv("HOME")
			}
			if home == "" {
				return nil, fmt.Errorf("home directory not found for known_hosts")
			}
			knownHostsFile = filepath.Join(home, ".ssh", "known_hosts")
		}
		if err := os.MkdirAll(filepath.Dir(knownHostsFile), 0700); err != nil {
			return nil, fmt.Errorf("create known_hosts dir: %w", err)
		}
		knownCb, err := knownhosts.New(knownHostsFile)
		if err != nil {
			// File doesn't exist — create it empty so we can write new hosts
			f, err := os.OpenFile(knownHostsFile, os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return nil, fmt.Errorf("create known_hosts: %w", err)
			}
			f.Close()
			knownCb, err = knownhosts.New(knownHostsFile)
			if err != nil {
				return nil, fmt.Errorf("init knownhosts: %w", err)
			}
		}
		hostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			err := knownCb(hostname, remote, key)
			if err != nil {
				// Known host with a changed key (MITM) — reject.
				if keyErr, ok := err.(*knownhosts.KeyError); ok && len(keyErr.Want) > 0 {
					return err
				}
				// Unknown host — REJECT by default (security). Pre-populate
				// ~/.ssh/known_hosts with the server's host key, or set
				// InsecureKnownHosts=true for first-time connections.
				return fmt.Errorf("unknown SFTP host %q: not in known_hosts. Pre-add the host key to ~/.ssh/known_hosts or set InsecureKnownHosts=true for first-time connections", hostname)
			}
			return nil
		}
	}

	config := &ssh.ClientConfig{
		User:            p.user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
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

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
