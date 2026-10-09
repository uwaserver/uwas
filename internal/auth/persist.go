package auth

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

func (m *Manager) secretFile() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "auth.json")
}

func (m *Manager) sessionsFile() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "sessions.json")
}

// loadOrCreateJWTSecret reads the persisted JWT signing key from disk, or
// generates a new one and persists it. Without persistence the secret rotates
// on every restart, which silently invalidates every active session.
func (m *Manager) loadOrCreateJWTSecret() error {
	path := m.secretFile()
	if path == "" {
		// Tests / no dataDir: generate ephemeral secret.
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		m.jwtSecret = secret
		return nil
	}

	data, err := os.ReadFile(path)
	if err == nil {
		var stored struct {
			JWTSecret []byte `json:"jwt_secret"`
		}
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("auth: parse %s: %w", path, err)
		}
		if len(stored.JWTSecret) >= 32 {
			m.jwtSecret = stored.JWTSecret
			return nil
		}
		// JWTSecret missing or too short — fall through to generate a new one.
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	m.jwtSecret = secret

	out, err := json.Marshal(struct {
		JWTSecret []byte `json:"jwt_secret"`
	}{JWTSecret: secret})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	// Creation mode does not change an existing temp file's permissions.
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(out); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadSessions reads persisted sessions from disk into m.sessions, dropping
// any that have already expired. Caller must NOT hold m.mu.
func (m *Manager) loadSessions() {
	path := m.sessionsFile()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var stored []*Session
	if err := json.Unmarshal(data, &stored); err != nil {
		return
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range stored {
		if s == nil || s.Token == "" {
			continue
		}
		if now.After(s.ExpiresAt) {
			continue
		}
		m.sessions[s.Token] = s
	}
}

// saveSessions persists the current session map to disk. Caller must NOT
// hold m.mu — this method takes a read lock to snapshot.
func (m *Manager) saveSessions() {
	m.mu.RLock()
	out := m.snapshotSessions()
	// Take the persist lock before releasing m.mu: any later mutation needs
	// m.mu exclusively, so its write queues behind this one.
	m.sessionsPersistMu.Lock()
	m.mu.RUnlock()
	defer m.sessionsPersistMu.Unlock()
	m.writeSessions(out)
}

// saveSessionsLocked is the same as saveSessions but assumes the caller
// already holds m.mu. Used by mutation paths (UpdateUser, DeleteUser, etc.)
// that invalidate sessions while holding the write lock.
func (m *Manager) saveSessionsLocked() {
	out := m.snapshotSessions()
	m.sessionsPersistMu.Lock()
	defer m.sessionsPersistMu.Unlock()
	m.writeSessions(out)
}

func (m *Manager) snapshotSessions() []*Session {
	now := time.Now()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s == nil || now.After(s.ExpiresAt) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (m *Manager) writeSessions(out []*Session) {
	path := m.sessionsFile()
	if path == "" {
		return
	}
	data, err := json.Marshal(out)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	// Creation mode does not change an existing temp file's permissions.
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return
	}
	if err := f.Close(); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("auth: save hosts failed: %v", err)
	}
}
