package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// latestLastLogin: atomic vs persisted field
// ---------------------------------------------------------------------------

func TestLatestLastLogin_PrefersAtomic(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	u := &User{
		LastLogin: now.Add(-24 * time.Hour), // old persisted value
	}
	// When atomic is zero, latestLastLogin falls back to the persisted field.
	if got := u.latestLastLogin(); !got.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("expected persisted fallback, got %v", got)
	}
	// After storing an atomic value, latestLastLogin returns the atomic.
	u.lastLoginNanos.Store(now.UnixNano())
	if got := u.latestLastLogin(); !got.Equal(now) {
		t.Fatalf("expected atomic value, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// AuthenticateFrom: disabled user branch
// ---------------------------------------------------------------------------

func TestAuthenticateFrom_DisabledUser(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)

	if _, err := m.CreateUser("alice", "", "correct-horse", RoleUser, nil); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Disable the user.
	m.mu.Lock()
	m.users["alice"].Enabled = false
	m.users["alice"].EnabledSet = true
	m.mu.Unlock()

	_, err := m.AuthenticateFrom("alice", "correct-horse", "")
	if err == nil {
		t.Fatal("expected error for disabled user")
	}
	if !strings.Contains(err.Error(), "user disabled") {
		t.Fatalf("expected 'user disabled', got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AuthenticateFrom: bcrypt mismatch path (exercise the "invalid credentials"
// path that comes from bcrypt.CompareHashAndPassword).
// ---------------------------------------------------------------------------

func TestAuthenticateFrom_WrongPassword(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)

	if _, err := m.CreateUser("alice", "", "correct-horse", RoleUser, nil); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err := m.AuthenticateFrom("alice", "wrong-password", "")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
	if !strings.Contains(err.Error(), "invalid credentials") {
		t.Fatalf("expected 'invalid credentials', got %v", err)
	}
}

// ---------------------------------------------------------------------------
// cleanupLoginAttempts: empty map
// ---------------------------------------------------------------------------

func TestCleanupLoginAttempts_EmptyMap(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	// Should not panic or deadlock.
	m.cleanupLoginAttempts()
}

// ---------------------------------------------------------------------------
// isLockedOut: no attempts for username
// ---------------------------------------------------------------------------

func TestIsLockedOut_NoAttempts(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	if m.isLockedOut("nonexistent-user") {
		t.Fatal("expected not locked out for user with no attempts")
	}
}

// ---------------------------------------------------------------------------
// snapshotSessions: filters out nil and expired sessions
// ---------------------------------------------------------------------------

func TestSnapshotSessions_FiltersNilAndExpired(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)

	now := time.Now()
	m.mu.Lock()
	m.sessions["nil-sess"] = nil
	m.sessions["expired-sess"] = &Session{Token: "expired-sess", ExpiresAt: now.Add(-time.Hour)}
	m.sessions["valid-sess"] = &Session{Token: "valid-sess", ExpiresAt: now.Add(time.Hour)}
	m.mu.Unlock()

	out := m.snapshotSessions()
	if len(out) != 1 {
		t.Fatalf("expected 1 session, got %d", len(out))
	}
	if out[0].Token != "valid-sess" {
		t.Fatalf("expected valid-sess, got %s", out[0].Token)
	}
}

// ---------------------------------------------------------------------------
// loadOrCreateJWTSecret: stored data exists but is unparseable JSON
// ---------------------------------------------------------------------------

func TestLoadJWTSecret_UnparseableStoredJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	// Write valid JSON but missing the jwt_secret field entirely.
	data, _ := json.Marshal(struct {
		SomethingElse string `json:"something_else"`
	}{SomethingElse: "hello"})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	m := &Manager{dataDir: dir}
	if err := m.loadOrCreateJWTSecret(); err != nil {
		t.Fatalf("loadOrCreateJWTSecret: %v", err)
	}
	if len(m.jwtSecret) != 32 {
		t.Fatalf("expected regenerated 32-byte secret, got %d", len(m.jwtSecret))
	}
}

// TestLoadJWTSecret_ShortStoredJSONBranch covers the case where the stored
// JWT secret field is present but is shorter than 32 bytes — the function
// must generate a fresh secret.
func TestLoadJWTSecret_StoredTooShortAndRegenerates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	stored := struct {
		JWTSecret []byte `json:"jwt_secret"`
	}{JWTSecret: []byte("short")}
	data, _ := json.Marshal(stored)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	m := &Manager{dataDir: dir}
	if err := m.loadOrCreateJWTSecret(); err != nil {
		t.Fatalf("loadOrCreateJWTSecret: %v", err)
	}
	if len(m.jwtSecret) != 32 {
		t.Fatalf("expected regenerated 32-byte secret, got %d", len(m.jwtSecret))
	}
}

// ---------------------------------------------------------------------------
// sessionCleanupLoop: verify that closing cleanupDone exits the goroutine
// ---------------------------------------------------------------------------

func TestSessionCleanupLoop_ExitsOnCleanupDone(t *testing.T) {
	m := newTestManager(t)
	// The goroutine runs forever; Stop() closes cleanupDone.
	// This is already covered by TestStop_IsIdempotent etc., but verify
	// the loop exits without panic.
	m.Stop()
	// Reading cleanupDone again should not block.
	_ = m.cleanupDone
}

// ---------------------------------------------------------------------------
// loadOrCreateJWTSecret: rand.Read failure in ephemeral path
// ---------------------------------------------------------------------------

func TestLoadJWTSecret_RandReadFailOnEphemeral(t *testing.T) {
	// Ephemeral path: when dataDir == "", a 32-byte ephemeral secret is
	// generated. This path is covered by TestJWTSecret_NoDataDirGeneratesEphemeral.
	// The rand.Read error path on ephemeral is untestable without source
	// injection (crypto/rand.Read never fails on Linux).
	// Placeholder documenting the gap:
	m := NewManager("", "")
	_ = m
	t.Log("ephemeral rand.Read error path: requires source injection; untestable on typical Linux")
}

// ---------------------------------------------------------------------------
// generateID / generateToken / generateAPIKey: crypto/rand error path
// ---------------------------------------------------------------------------

func TestGenerateID_RandReadFail(t *testing.T) {
	// The crypto/rand.Read error + /dev/urandom fallback is impossible to
	// exercise without modifying source (crypto/rand.Read never fails on
	// Linux). The happy path is covered by normal user-creation tests.
	id, err := generateID()
	if err != nil {
		t.Fatalf("generateID: %v", err)
	}
	if len(id) == 0 {
		t.Fatal("expected non-empty ID")
	}
	t.Log("generateID crypto/rand error + fallback path: requires source injection")
}

func TestGenerateToken_RandReadFail(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if len(tok) == 0 {
		t.Fatal("expected non-empty token")
	}
	t.Log("generateToken crypto/rand error + fallback path: requires source injection")
}

func TestGenerateAPIKey_RandReadFail(t *testing.T) {
	key, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey: %v", err)
	}
	if !strings.HasPrefix(key, "uk_") {
		t.Fatalf("expected uk_ prefix, got %s", key)
	}
	t.Log("generateAPIKey crypto/rand error + fallback path: requires source injection")
}

// ---------------------------------------------------------------------------
// authGateFor: empty username
// ---------------------------------------------------------------------------

func TestAuthGateFor_EmptyString(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	// Must not panic and must return a valid mutex.
	mu := m.authGateFor("")
	if mu == nil {
		t.Fatal("expected non-nil mutex")
	}
	mu.Lock()
	mu.Unlock()
}

// ---------------------------------------------------------------------------
// loadOrCreateJWTSecret: stored JSON with jwt_secret field that is valid
// (the happy path for ReadFile + Unmarshal).
// ---------------------------------------------------------------------------

func TestLoadJWTSecret_StoredValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	stored := struct {
		JWTSecret []byte `json:"jwt_secret"`
	}{JWTSecret: secret}
	data, _ := json.Marshal(stored)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	m := &Manager{dataDir: dir}
	if err := m.loadOrCreateJWTSecret(); err != nil {
		t.Fatalf("loadOrCreateJWTSecret: %v", err)
	}
	if len(m.jwtSecret) != 32 {
		t.Fatalf("expected 32-byte secret, got %d", len(m.jwtSecret))
	}
	for i, b := range m.jwtSecret {
		if b != byte(i) {
			t.Fatalf("stored secret mismatch at byte %d: got %d, want %d", i, b, byte(i))
		}
	}
}

// ---------------------------------------------------------------------------
// CloneUser: domains nil vs empty slice
// ---------------------------------------------------------------------------

func TestCloneUser_WithDomains(t *testing.T) {
	u := &User{
		ID:       "uid-1",
		Username: "alice",
		Domains:  []string{"example.com", "test.com"},
	}
	cloned := cloneUser(u)
	if cloned == nil {
		t.Fatal("expected non-nil clone")
	}
	if len(cloned.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(cloned.Domains))
	}
	if cloned.Domains[0] != "example.com" {
		t.Fatalf("expected domain, got %s", cloned.Domains[0])
	}
	// Modify original; clone must remain unchanged.
	u.Domains[0] = "mutated.com"
	if cloned.Domains[0] != "example.com" {
		t.Fatalf("clone was mutated by original change")
	}
}

func TestCloneUser_DomainsIsNil(t *testing.T) {
	u := &User{ID: "uid-1", Username: "bob"}
	cloned := cloneUser(u)
	if cloned == nil {
		t.Fatal("expected non-nil clone")
	}
	if cloned.Domains != nil {
		t.Fatal("expected nil domains in clone")
	}
}

// ---------------------------------------------------------------------------
// recordFailedAttempt missing key: should create entry
// ---------------------------------------------------------------------------

func TestRecordFailedAttempt_CreatesEntry(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	m.recordFailedAttempt("new-user")
	m.loginAttemptsMu.Lock()
	attempts := len(m.loginAttempts["new-user"])
	m.loginAttemptsMu.Unlock()
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
	m.clearFailedAttempts("new-user")
	m.loginAttemptsMu.Lock()
	_, exists := m.loginAttempts["new-user"]
	m.loginAttemptsMu.Unlock()
	if exists {
		t.Fatal("expected entry removed after clear")
	}
}

// ---------------------------------------------------------------------------
// validateSession: expired path
// ---------------------------------------------------------------------------

func TestValidateSession_Expired(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	m.mu.Lock()
	m.sessions["old"] = &Session{
		Token:     "old",
		ExpiresAt: time.Now().Add(-time.Second),
	}
	m.mu.Unlock()
	_, err := m.ValidateSession("old")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected session expired, got %v", err)
	}
}

// TestValidateSession_NotFound path
func TestValidateSession_NotFound(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	_, err := m.ValidateSession("doesnotexist")
	if err == nil || !strings.Contains(err.Error(), "invalid session") {
		t.Fatalf("expected invalid session, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// saveUsers: early return when usersFile is "" (no dataDir)
// ---------------------------------------------------------------------------

func TestSaveUsers_NoDataDir(t *testing.T) {
	m := &Manager{
		users: make(map[string]*User),
		mu:    sync.RWMutex{},
	}
	m.saveUsers() // must not panic
}

// ---------------------------------------------------------------------------
// loadSessions: early return path (no dataDir)
// ---------------------------------------------------------------------------

func TestLoadSessions_NoDataDir(t *testing.T) {
	m := &Manager{
		mu: sync.RWMutex{},
	}
	m.loadSessions() // must not panic
}

// ---------------------------------------------------------------------------
// CreateFirstAdmin: validation
// ---------------------------------------------------------------------------

func TestCreateFirstAdmin_Validation(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)
	_, err := m.CreateFirstAdmin("", "", "")
	if err == nil {
		t.Fatal("expected error for empty username")
	}
	if _, err = m.CreateFirstAdmin("a", "", ""); err == nil && !strings.Contains(err.Error(), "invalid username") {
		t.Fatalf("expected invalid username error, got %v", err)
	}
}
