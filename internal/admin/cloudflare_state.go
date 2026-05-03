package admin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// cloudflareStateFile returns the absolute path of the on-disk state file.
// Empty if the server has no configPath (e.g. tests). Mode 0600 on write.
func (s *Server) cloudflareStateFile() string {
	if s.configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.configPath), "cloudflare.json")
}

// loadCloudflareState reads the persisted Cloudflare config from disk into
// the cloudflareConfig global. Missing file is not an error.
func (s *Server) loadCloudflareState() error {
	path := s.cloudflareStateFile()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var st cloudflareState
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	if st.Tunnels == nil {
		st.Tunnels = []cloudflareTunnel{}
	}
	cloudflareMu.Lock()
	cloudflareConfig = &st
	cloudflareMu.Unlock()
	return nil
}

// saveCloudflareStateLocked persists the current cloudflareConfig to disk.
// Caller must hold cloudflareMu (read or write). nil config deletes the file.
func (s *Server) saveCloudflareStateLocked() error {
	path := s.cloudflareStateFile()
	if path == "" {
		return nil
	}
	if cloudflareConfig == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(cloudflareConfig, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// persistCloudflareState saves under its own short-lived lock. Use when
// the caller has already released cloudflareMu.
func (s *Server) persistCloudflareState() {
	cloudflareMu.RLock()
	err := s.saveCloudflareStateLocked()
	cloudflareMu.RUnlock()
	if err != nil && s.logger != nil {
		s.logger.Error("cloudflare state persist failed", "err", err.Error())
	}
}
