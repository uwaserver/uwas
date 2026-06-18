package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Authenticate: lockout branch
// ---------------------------------------------------------------------------

// TestAuthenticateLockout drives the brute-force lockout path: after
// maxLoginAttempts failures within the window, Authenticate must reject
// even a correct password with the lockout error.
func TestAuthenticateLockout(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)

	if _, err := m.CreateUser("alice", "", "correct-horse", RoleAdmin, nil); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Exhaust the allowed attempts with wrong passwords.
	for i := 0; i < maxLoginAttempts; i++ {
		if _, err := m.Authenticate("alice", "wrong"); err == nil {
			t.Fatalf("attempt %d: expected failure", i)
		}
	}

	// Now even the correct password must be rejected by the lockout guard.
	_, err := m.Authenticate("alice", "correct-horse")
	if err == nil {
		t.Fatal("expected lockout error after max failed attempts")
	}
	if got := err.Error(); got != "too many failed attempts; try again later" {
		t.Fatalf("unexpected error: %q", got)
	}

	// Clearing attempts lets the user back in (also exercises clearFailedAttempts).
	m.clearFailedAttempts("alice")
	if _, err := m.Authenticate("alice", "correct-horse"); err != nil {
		t.Fatalf("expected success after clearing attempts: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AuthenticateAPIKey: legacy scan continue branch
// ---------------------------------------------------------------------------

// TestAuthenticateAPIKeyLegacyScanSkipsIneligible exercises the `continue`
// in the legacy plaintext scan loop: users that are disabled, already
// hashed, or have no plaintext key must be skipped before the eligible
// legacy user is matched.
func TestAuthenticateAPIKeyLegacyScanSkipsIneligible(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)

	// Eligible legacy user (plaintext key, no hash).
	legacy, _ := m.CreateUser("legacy", "", "pw", RoleAdmin, nil)
	plaintext := legacy.FullAPIKey

	// Ineligible: a normal hashed user (APIKeyHash != "") — exercises the
	// `user.APIKeyHash != ""` skip.
	if _, err := m.CreateUser("hashed", "", "pw", RoleUser, nil); err != nil {
		t.Fatalf("CreateUser hashed: %v", err)
	}
	// Ineligible: disabled legacy user — exercises the `!user.Enabled` skip.
	if _, err := m.CreateUser("disabledlegacy", "", "pw", RoleUser, nil); err != nil {
		t.Fatalf("CreateUser disabledlegacy: %v", err)
	}

	m.mu.Lock()
	// Convert "legacy" to legacy plaintext state.
	for k, u := range m.usersByAPIKeyHash {
		if u.Username == "legacy" {
			delete(m.usersByAPIKeyHash, k)
		}
	}
	m.users["legacy"].APIKey = plaintext
	m.users["legacy"].APIKeyHash = ""
	// Convert "disabledlegacy" to legacy plaintext but disabled, with no
	// API key set at all to also hit the `user.APIKey == ""` skip.
	m.users["disabledlegacy"].Enabled = false
	m.users["disabledlegacy"].APIKeyHash = ""
	m.users["disabledlegacy"].APIKey = ""
	m.mu.Unlock()

	m.SetAllowLegacyPlaintextKey(true)

	u, err := m.AuthenticateAPIKey(plaintext)
	if err != nil {
		t.Fatalf("expected legacy plaintext auth to succeed: %v", err)
	}
	if u.Username != "legacy" {
		t.Fatalf("expected legacy, got %s", u.Username)
	}
}

// TestAuthenticateAPIKeyLegacyScanNoMatch forces the legacy scan to iterate
// every entry and hit the `continue` for each skip-eligible user, then fall
// through to the "invalid API key" return because nothing matches. This
// deterministically covers the continue branch regardless of map order
// (every user in the map is skip-eligible).
func TestAuthenticateAPIKeyLegacyScanNoMatch(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(m.Stop)

	// All users are skip-eligible: hashed (APIKeyHash != "").
	for _, name := range []string{"user1", "user2", "user3"} {
		if _, err := m.CreateUser(name, "", "pw", RoleUser, nil); err != nil {
			t.Fatalf("CreateUser %s: %v", name, err)
		}
	}
	m.SetAllowLegacyPlaintextKey(true)

	// No plaintext key can match (every user is hashed), so the loop visits
	// all entries via `continue` and AuthenticateAPIKey returns an error.
	if _, err := m.AuthenticateAPIKey("uk_nonexistent-key"); err == nil {
		t.Fatal("expected invalid API key error")
	}
}

// ---------------------------------------------------------------------------
// NewManager panics when the JWT secret cannot be initialized
// ---------------------------------------------------------------------------

func TestNewManagerJWTInitPanics(t *testing.T) {
	dir := t.TempDir()
	// Make auth.json a directory so loadOrCreateJWTSecret's ReadFile returns
	// a non-ErrNotExist error → returns err → NewManager panics.
	if err := os.MkdirAll(filepath.Join(dir, "auth.json"), 0700); err != nil {
		t.Fatalf("mkdir auth.json: %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected NewManager to panic on JWT init failure")
		}
	}()
	_ = NewManager(dir, "")
}

// ---------------------------------------------------------------------------
// writeSessions: WriteFile (temp) error
// ---------------------------------------------------------------------------

// TestWriteSessionsWriteFileError makes the temp target a directory so the
// os.WriteFile(tmp, ...) call inside writeSessions fails.
func TestWriteSessionsWriteFileError(t *testing.T) {
	dir := t.TempDir()
	// writeSessions writes to sessions.json.tmp first; make that a directory.
	if err := os.MkdirAll(filepath.Join(dir, "sessions.json.tmp"), 0700); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	m := &Manager{dataDir: dir}
	m.writeSessions([]*Session{{Token: "t", ExpiresAt: time.Now().Add(time.Hour)}})
	// sessions.json must not have been created (rename never reached).
	if _, err := os.Stat(filepath.Join(dir, "sessions.json")); err == nil {
		t.Fatal("sessions.json should not exist")
	}
}

// ---------------------------------------------------------------------------
// cloneUser(nil)
// ---------------------------------------------------------------------------

func TestCloneUserNil(t *testing.T) {
	if got := cloneUser(nil); got != nil {
		t.Fatalf("cloneUser(nil) = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// Stop with nil cleanupDone (manager built without NewManager)
// ---------------------------------------------------------------------------

func TestStopNilCleanupDone(t *testing.T) {
	// A zero-value Manager has cleanupDone == nil; Stop must no-op safely.
	m := &Manager{}
	m.Stop() // must not panic
}

// ---------------------------------------------------------------------------
// saveUsers: MkdirAll error path + lastLoginNanos sync
// ---------------------------------------------------------------------------

// TestSaveUsersMkdirError forces saveUsers' MkdirAll to fail by making the
// parent of dataDir a regular file, so MkdirAll(dir) cannot create it.
func TestSaveUsersMkdirError(t *testing.T) {
	base := t.TempDir()
	// Create a file where a directory component needs to be.
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	// dataDir is under the file → MkdirAll(dataDir) fails (not a directory).
	dataDir := filepath.Join(blocker, "data")

	m := &Manager{
		users:             make(map[string]*User),
		usersByID:         make(map[string]*User),
		usersByAPIKeyHash: make(map[string]*User),
		sessions:          make(map[string]*Session),
		loginAttempts:     make(map[string][]time.Time),
		dataDir:           dataDir,
	}
	u := &User{ID: "1", Username: "x", Enabled: true}
	u.lastLoginNanos.Store(time.Now().UnixNano()) // exercise the sync branch
	m.users["x"] = u

	m.saveUsers() // must hit MkdirAll error and return without panicking

	if _, err := os.Stat(filepath.Join(dataDir, "users.json")); err == nil {
		t.Fatal("users.json should not have been written")
	}
}

// TestSaveUsersSyncsLastLogin verifies the lastLoginNanos→LastLogin sync in
// saveUsers writes the timestamp into the persisted JSON.
func TestSaveUsersSyncsLastLogin(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager2(t, dir)
	t.Cleanup(m.Stop)

	if _, err := m.CreateUser("alice", "", "pw", RoleAdmin, nil); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := m.Authenticate("alice", "pw"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	// Authenticate updates lastLoginNanos but does not itself persist users.
	// Trigger a saveUsers (which syncs the atomic into LastLogin) via an
	// update, then re-load and confirm LastLogin landed in the JSON.
	if err := m.UpdateUser("alice", &User{Email: "alice@example.com"}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	m2 := newTestManager2(t, dir)
	t.Cleanup(m2.Stop)
	got, ok := m2.GetUser("alice")
	if !ok {
		t.Fatal("alice missing after reload")
	}
	if got.LastLogin.IsZero() {
		t.Fatal("LastLogin not persisted")
	}
}

func newTestManager2(t *testing.T, dir string) *Manager {
	t.Helper()
	return NewManager(dir, "k")
}

// ---------------------------------------------------------------------------
// hasPathPrefixBoundary: trailing-slash prefix branch
// ---------------------------------------------------------------------------

func TestHasPathPrefixBoundaryTrailingSlash(t *testing.T) {
	if !hasPathPrefixBoundary("/foo/bar", "/foo/") {
		t.Fatal("expected match for trailing-slash prefix")
	}
	if hasPathPrefixBoundary("/foobar", "/foo/") {
		t.Fatal("did not expect match for trailing-slash prefix")
	}
}

// ---------------------------------------------------------------------------
// loadOrCreateJWTSecret: ReadFile error that is not ErrNotExist
// ---------------------------------------------------------------------------

// TestLoadJWTSecretReadError makes the secret path a directory so ReadFile
// returns a non-ErrNotExist error, which loadOrCreateJWTSecret must surface
// (NewManager panics in that case).
func TestLoadJWTSecretReadError(t *testing.T) {
	dir := t.TempDir()
	// auth.json is the secret file; make it a directory so ReadFile errors.
	if err := os.MkdirAll(filepath.Join(dir, "auth.json"), 0700); err != nil {
		t.Fatalf("mkdir auth.json: %v", err)
	}

	m := &Manager{dataDir: dir}
	err := m.loadOrCreateJWTSecret()
	if err == nil {
		t.Fatal("expected error when secret file is a directory")
	}
}

// TestLoadJWTSecretMkdirError forces the create branch's MkdirAll to fail by
// making the data dir's parent a file.
func TestLoadJWTSecretMkdirError(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "f")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	dataDir := filepath.Join(blocker, "sub")
	m := &Manager{dataDir: dataDir}
	if err := m.loadOrCreateJWTSecret(); err == nil {
		t.Fatal("expected MkdirAll error")
	}
}

// TestLoadJWTSecretShortStoredRegenerates covers the path where a stored
// secret is too short (<32 bytes), so a new one is generated and persisted.
func TestLoadJWTSecretShortStoredRegenerates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	data, _ := json.Marshal(struct {
		JWTSecret []byte `json:"jwt_secret"`
	}{JWTSecret: []byte("short")})
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

// TestLoadJWTSecretWriteError covers the create branch's WriteFile failure:
// a read-only (but existing) data dir lets ReadFile return ErrNotExist and
// MkdirAll succeed (dir already present), but WriteFile of the temp file
// fails with EACCES. Skipped when running as root (perms are bypassed).
func TestLoadJWTSecretWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are bypassed")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) }) // allow TempDir cleanup

	m := &Manager{dataDir: dir}
	if err := m.loadOrCreateJWTSecret(); err == nil {
		t.Fatal("expected WriteFile error in read-only data dir")
	}
}

// ---------------------------------------------------------------------------
// loadSessions: invalid JSON, nil/empty-token skip, expired skip
// ---------------------------------------------------------------------------

func TestLoadSessionsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	m := NewManager(dir, "")
	t.Cleanup(m.Stop)
	m.mu.RLock()
	n := len(m.sessions)
	m.mu.RUnlock()
	if n != 0 {
		t.Fatalf("expected no sessions from invalid JSON, got %d", n)
	}
}

func TestLoadSessionsSkipsNilEmptyAndExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stored := []*Session{
		nil, // skipped: nil
		{Token: "", ExpiresAt: now.Add(time.Hour)},         // skipped: empty token
		{Token: "expired", ExpiresAt: now.Add(-time.Hour)}, // skipped: expired
		{Token: "good", ExpiresAt: now.Add(time.Hour)},     // kept
	}
	data, _ := json.Marshal(stored)
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	m := NewManager(dir, "")
	t.Cleanup(m.Stop)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(m.sessions))
	}
	if _, ok := m.sessions["good"]; !ok {
		t.Fatal("expected 'good' session to survive")
	}
}

// ---------------------------------------------------------------------------
// writeSessions: empty path (no dataDir) + MkdirAll error
// ---------------------------------------------------------------------------

func TestWriteSessionsNoDataDir(t *testing.T) {
	m := &Manager{} // dataDir == "" → sessionsFile() == "" → early return
	m.writeSessions([]*Session{{Token: "t", ExpiresAt: time.Now().Add(time.Hour)}})
	// Nothing to assert beyond not panicking / not writing anywhere.
}

func TestWriteSessionsMkdirError(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "f")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	m := &Manager{dataDir: filepath.Join(blocker, "sub")}
	// MkdirAll under a file must fail; writeSessions must return silently.
	m.writeSessions([]*Session{{Token: "t", ExpiresAt: time.Now().Add(time.Hour)}})
	if _, err := os.Stat(filepath.Join(blocker, "sub", "sessions.json")); err == nil {
		t.Fatal("sessions.json should not exist")
	}
}
