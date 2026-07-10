// Package auth provides multi-user authentication and authorization.
// Supports admin, reseller, and user roles with scoped permissions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role represents a user role with specific permissions.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleReseller Role = "reseller"
	RoleUser     Role = "user"
)

// User represents a system user with authentication credentials.
type User struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Email      string    `json:"email"`
	Password   string    `json:"password_hash,omitempty"` // bcrypt hash
	Role       Role      `json:"role"`
	Domains    []string  `json:"domains,omitempty"`      // For resellers: managed domains
	APIKey     string    `json:"api_key,omitempty"`      // Display prefix only (first 8 chars)
	APIKeyHash string    `json:"api_key_hash,omitempty"` // SHA256 hash of full API key
	FullAPIKey string    `json:"-"`                      // Full key, set only at generation time (not persisted)
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastLogin  time.Time `json:"last_login,omitempty"`
	EnabledSet bool      `json:"-"`

	// lastLoginNanos is the live, lock-free source of truth for LastLogin
	// after the user has been authenticated this process. The LastLogin field
	// holds the last persisted value (used for serialization + tests that read
	// it as a plain field on a cloned User snapshot). Updated by Authenticate;
	// synced back into LastLogin by cloneUser and saveUsers (was P13).
	lastLoginNanos atomic.Int64 `json:"-"`
}

// latestLastLogin returns the most recent login time for u, preferring the
// atomic value (live updates) over the persisted LastLogin field.
func (u *User) latestLastLogin() time.Time {
	if n := u.lastLoginNanos.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return u.LastLogin
}

// Session represents an authenticated session.
type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	Domains   []string  `json:"domains,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Permission represents a specific action that can be performed.
type Permission string

const (
	PermDomainRead   Permission = "domain:read"
	PermDomainCreate Permission = "domain:create"
	PermDomainUpdate Permission = "domain:update"
	PermDomainDelete Permission = "domain:delete"
	PermUserRead     Permission = "user:read"
	PermUserCreate   Permission = "user:create"
	PermUserUpdate   Permission = "user:update"
	PermUserDelete   Permission = "user:delete"
	PermSystemRead   Permission = "system:read"
	PermSystemConfig Permission = "system:config"
	PermBackupManage Permission = "backup:manage"
	PermCertManage   Permission = "cert:manage"
)

// rolePermissions defines permissions for each role.
var rolePermissions = map[Role][]Permission{
	RoleAdmin: {
		PermDomainRead, PermDomainCreate, PermDomainUpdate, PermDomainDelete,
		PermUserRead, PermUserCreate, PermUserUpdate, PermUserDelete,
		PermSystemRead, PermSystemConfig,
		PermBackupManage, PermCertManage,
	},
	RoleReseller: {
		PermDomainRead, PermDomainCreate, PermDomainUpdate, PermDomainDelete,
		PermUserRead,
		PermSystemRead,
		PermCertManage,
	},
	RoleUser: {
		PermDomainRead,
		PermSystemRead,
	},
}

// Manager handles user authentication and authorization.
type Manager struct {
	mu        sync.RWMutex
	users     map[string]*User // key: username
	usersByID map[string]*User // key: user ID
	// usersByAPIKeyHash maps SHA256(api-key) → user for O(1) lookup in
	// AuthenticateAPIKey. Only populated for users that have an
	// APIKeyHash (legacy plaintext-only users still require the scan
	// fallback). Refs: refactor.md P8.
	usersByAPIKeyHash map[string]*User
	sessions          map[string]*Session // key: token
	dataDir           string
	apiKey            string // Global admin API key (backward compat)
	jwtSecret         []byte

	// allowLegacyPlaintextKey gates the v0.1 plaintext API-key fallback
	// in AuthenticateAPIKey. Off by default from v0.5; operators with
	// un-rehashed legacy users opt in via the
	// users.allow_legacy_plaintext_api_key config flag and are warned
	// to rotate. Refs: refactor.md A16.
	allowLegacyPlaintextKey atomic.Bool

	// Brute-force protection: tracks failed login attempts per username.
	loginAttemptsMu sync.Mutex
	loginAttempts   map[string][]time.Time // username -> timestamps of failed attempts

	// authGates serializes authentication attempts per username so the lockout
	// check and the failure-count increment around the slow bcrypt compare form
	// one critical section. Sharded by username hash to bound memory (an
	// unbounded per-username map would leak on attacker-supplied usernames).
	authGates [authGateShards]sync.Mutex

	// sessionTTL is the session lifetime; 0 means use the 24h default. Set from
	// global.users.session_ttl via SetSessionTTL.
	sessionTTL time.Duration

	// Background session pruner. Closed by Stop(). Nil when sessionCleanupInterval
	// is 0 (e.g. tests that want full control).
	cleanupDone chan struct{}
}

// SetSessionTTL configures the session lifetime (hours). hours <= 0 keeps the
// 24h default. Wires up the previously-ignored global.users.session_ttl.
func (m *Manager) SetSessionTTL(hours int) {
	if hours > 0 {
		m.sessionTTL = time.Duration(hours) * time.Hour
	}
}

// sessionLifetime returns the configured session TTL, or 24h if unset.
func (m *Manager) sessionLifetime() time.Duration {
	if m.sessionTTL > 0 {
		return m.sessionTTL
	}
	return 24 * time.Hour
}

// sessionCleanupInterval is how often the background goroutine sweeps the
// session map for expired entries. One hour is well below the 24h session
// lifetime, so the worst-case leak is ~1h of expired sessions in memory.
const sessionCleanupInterval = 1 * time.Hour

// NewManager creates a new auth manager.
func NewManager(dataDir, globalAPIKey string) *Manager {
	m := &Manager{
		users:             make(map[string]*User),
		usersByID:         make(map[string]*User),
		usersByAPIKeyHash: make(map[string]*User),
		sessions:          make(map[string]*Session),
		loginAttempts:     make(map[string][]time.Time),
		dataDir:           dataDir,
		apiKey:            globalAPIKey,
		cleanupDone:       make(chan struct{}),
	}
	if err := m.loadOrCreateJWTSecret(); err != nil {
		panic("auth: jwt secret init failed: " + err.Error())
	}
	m.loadUsers()
	m.loadSessions()
	go m.sessionCleanupLoop()
	return m
}

// SetAllowLegacyPlaintextKey toggles the legacy plaintext API-key
// fallback path. The flag is read inside AuthenticateAPIKey on every
// admin/MCP request, so it is stored as an atomic.Bool to allow live
// reload without coordinating with auth-side locks.
func (m *Manager) SetAllowLegacyPlaintextKey(allow bool) {
	m.allowLegacyPlaintextKey.Store(allow)
}

// Stop signals the background session-cleanup goroutine to exit. Safe to
// call multiple times. If never called, the goroutine simply lives until
// the process exits.
func (m *Manager) Stop() {
	if m.cleanupDone == nil {
		return
	}
	select {
	case <-m.cleanupDone:
		// already closed
	default:
		close(m.cleanupDone)
	}
}

// sessionCleanupLoop wakes every sessionCleanupInterval and removes
// expired sessions from memory and disk plus stale brute-force login
// attempt entries. Without this, both maps would grow unbounded over
// time (sessions: expired entries never pruned on write; loginAttempts:
// every distinct attacker-supplied username keeps an empty slice
// forever).
func (m *Manager) sessionCleanupLoop() {
	t := time.NewTicker(sessionCleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-m.cleanupDone:
			return
		case <-t.C:
			m.CleanupSessions()
			m.cleanupLoginAttempts()
		}
	}
}

// cleanupLoginAttempts deletes loginAttempts entries whose timestamps
// have all aged out of the lockout window. Counterpart to isLockedOut,
// which trims a single user's slice but never removes the map key.
func (m *Manager) cleanupLoginAttempts() {
	m.loginAttemptsMu.Lock()
	defer m.loginAttemptsMu.Unlock()
	cutoff := time.Now().Add(-loginLockoutWindow)
	for username, attempts := range m.loginAttempts {
		stillRecent := false
		for _, t := range attempts {
			if t.After(cutoff) {
				stillRecent = true
				break
			}
		}
		if !stillRecent {
			delete(m.loginAttempts, username)
		}
	}
}

const (
	maxLoginAttempts   = 5
	loginLockoutWindow = 15 * time.Minute
	authGateShards     = 256
)

// authGateFor returns the per-username serialization mutex. Collisions across
// the shard set only add harmless extra serialization (which also slows
// brute-forcing), never correctness issues.
func (m *Manager) authGateFor(username string) *sync.Mutex {
	var h uint32 = 2166136261 // FNV-1a 32-bit
	for i := 0; i < len(username); i++ {
		h ^= uint32(username[i])
		h *= 16777619
	}
	return &m.authGates[h%authGateShards]
}

// isLockedOut returns true if the username has exceeded maxLoginAttempts
// failed attempts within loginLockoutWindow.
func (m *Manager) isLockedOut(username string) bool {
	m.loginAttemptsMu.Lock()
	defer m.loginAttemptsMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-loginLockoutWindow)
	attempts := m.loginAttempts[username]
	var recent []time.Time
	for _, t := range attempts {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	m.loginAttempts[username] = recent
	return len(recent) >= maxLoginAttempts
}

// recordFailedAttempt records a failed login attempt for the username.
func (m *Manager) recordFailedAttempt(username string) {
	m.loginAttemptsMu.Lock()
	defer m.loginAttemptsMu.Unlock()
	m.loginAttempts[username] = append(m.loginAttempts[username], time.Now())
}

// clearFailedAttempts clears failed login attempts for the username.
func (m *Manager) clearFailedAttempts(username string) {
	m.loginAttemptsMu.Lock()
	defer m.loginAttemptsMu.Unlock()
	delete(m.loginAttempts, username)
}

// CreateUser creates a new user.
func (m *Manager) CreateUser(username, email, password string, role Role, domains []string) (*User, error) {
	if username == "" || password == "" {
		return nil, errors.New("username and password required")
	}

	if !isValidRole(role) {
		return nil, errors.New("invalid role")
	}

	// Validate username (alphanumeric, underscore, hyphen)
	if !isValidUsername(username) {
		return nil, errors.New("invalid username format")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createUserLocked(username, email, password, role, domains)
}

// CreateFirstAdmin atomically creates the first admin during bootstrap. The
// "no users yet" check and the insert happen under one lock, so two concurrent
// first-run requests can't both create an admin (VULN-027 TOCTOU).
func (m *Manager) CreateFirstAdmin(username, email, password string) (*User, error) {
	if username == "" || password == "" {
		return nil, errors.New("username and password required")
	}
	if !isValidUsername(username) {
		return nil, errors.New("invalid username format")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) != 0 {
		return nil, errors.New("bootstrap is already complete")
	}
	return m.createUserLocked(username, email, password, RoleAdmin, nil)
}

// createUserLocked builds and persists a user. m.mu must be held.
func (m *Manager) createUserLocked(username, email, password string, role Role, domains []string) (*User, error) {
	if _, exists := m.users[username]; exists {
		return nil, errors.New("user already exists")
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// Generate API key
	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("generate API key: %w", err)
	}
	apiKeyPrefix := apiKey
	if len(apiKeyPrefix) > 8 {
		apiKeyPrefix = apiKeyPrefix[:8]
	}

	userID, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("generate user ID: %w", err)
	}

	user := &User{
		ID:         userID,
		Username:   username,
		Email:      email,
		Password:   string(hash),
		Role:       role,
		Domains:    domains,
		APIKey:     apiKeyPrefix,
		APIKeyHash: hashAPIKey(apiKey),
		FullAPIKey: apiKey,
		Enabled:    true,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	m.users[username] = user
	m.usersByID[user.ID] = user
	if user.APIKeyHash != "" {
		m.usersByAPIKeyHash[user.APIKeyHash] = user
	}
	m.saveUsers()

	return cloneUser(user), nil
}

// decoyHash is a valid bcrypt hash (computed once, at DefaultCost) used to
// equalize the timing of the "user does not exist" path with a real password
// compare — otherwise the missing-user early return is measurably faster and
// leaks which usernames exist (VULN-025).
var decoyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("uwas-timing-equalizer-decoy"), bcrypt.DefaultCost)
	return h
})

// lockoutKey scopes brute-force lockout to (username, IP) so a flood from one
// IP can't lock a legitimate operator out from another IP (targeted-lockout
// DoS), while still capping per-source guessing. Distributed (many-IP) brute
// force is covered by the minimum-password-length policy. An empty IP falls
// back to a username-only key (callers that don't supply a client IP).
func lockoutKey(username, clientIP string) string {
	if clientIP == "" {
		return username
	}
	return username + "|" + clientIP
}

// Authenticate validates credentials and returns a session. Prefer
// AuthenticateFrom so brute-force lockout is scoped per (username, IP).
func (m *Manager) Authenticate(username, password string) (*Session, error) {
	return m.AuthenticateFrom(username, password, "")
}

// AuthenticateFrom validates credentials, scoping brute-force lockout to the
// supplied client IP (see lockoutKey).
func (m *Manager) AuthenticateFrom(username, password, clientIP string) (*Session, error) {
	lockKey := lockoutKey(username, clientIP)

	// Serialize attempts for this (username, IP) so the lockout check below and
	// the failure increment after the bcrypt compare are atomic together —
	// without this, a concurrent burst all passes isLockedOut before any records
	// a failure, admitting more than maxLoginAttempts tries.
	gate := m.authGateFor(lockKey)
	gate.Lock()
	defer gate.Unlock()

	if m.isLockedOut(lockKey) {
		return nil, errors.New("too many failed attempts; try again later")
	}

	m.mu.RLock()
	user, exists := m.users[username]
	if !exists {
		m.mu.RUnlock()
		// Spend the same bcrypt time as the real path to avoid leaking, via
		// response timing, whether the username exists.
		_ = bcrypt.CompareHashAndPassword(decoyHash(), []byte(password))
		m.recordFailedAttempt(lockKey)
		return nil, errors.New("invalid credentials")
	}
	// Snapshot fields under the lock to avoid racing with UpdateUser.
	enabled := user.Enabled
	passwordHash := user.Password
	userID := user.ID
	role := user.Role
	domains := make([]string, len(user.Domains))
	copy(domains, user.Domains)
	m.mu.RUnlock()

	if !enabled {
		m.recordFailedAttempt(lockKey)
		return nil, errors.New("user disabled")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		m.recordFailedAttempt(lockKey)
		return nil, errors.New("invalid credentials")
	}

	m.clearFailedAttempts(lockKey)

	// Update last login lock-free. The atomic value is preferred by
	// latestLastLogin and synced back into User.LastLogin by cloneUser and
	// saveUsers, so observers downstream still see the fresh timestamp.
	user.lastLoginNanos.Store(time.Now().UnixNano())

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}

	session := &Session{
		Token:     token,
		UserID:    userID,
		Username:  username,
		Role:      role,
		Domains:   domains,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(m.sessionLifetime()),
	}

	m.mu.Lock()
	m.sessions[session.Token] = session
	m.mu.Unlock()
	m.saveSessions()

	return session, nil
}

// AuthenticateAPIKey validates an API key and returns the user.
func (m *Manager) AuthenticateAPIKey(key string) (*User, error) {
	// Check global API key first (backward compatibility)
	if m.apiKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(m.apiKey)) == 1 {
		return &User{
			ID:       "admin",
			Username: "admin",
			Role:     RoleAdmin,
			Enabled:  true,
		}, nil
	}

	// Hash the incoming key for comparison
	keyHash := hashAPIKey(key)

	// Check per-user API keys
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Fast path: O(1) lookup via secondary index. Covers every user
	// authenticated under the current SHA256-hash scheme.
	if user, ok := m.usersByAPIKeyHash[keyHash]; ok && user.Enabled {
		// Re-verify under ConstantTimeCompare to keep equal-length compare
		// semantics; the map probe itself is short-circuiting, but the
		// SHA256 input that produced the key is already the secret.
		if subtle.ConstantTimeCompare([]byte(keyHash), []byte(user.APIKeyHash)) == 1 {
			return cloneUser(user), nil
		}
	}

	// Backward compatibility: scan only legacy plaintext entries that have
	// not yet been rehashed. Gated by the
	// users.allow_legacy_plaintext_api_key config flag (off by default
	// from v0.5; planned for removal). Operators with un-rotated keys
	// must opt in and rotate before the flag stays off.
	// Refs: refactor.md A16.
	if m.allowLegacyPlaintextKey.Load() {
		for _, user := range m.users {
			if !user.Enabled || user.APIKeyHash != "" || user.APIKey == "" {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(key), []byte(user.APIKey)) == 1 {
				slog.Warn("API key authenticated via legacy plaintext comparison; users.allow_legacy_plaintext_api_key is on, rotate keys and disable",
					"user", user.Username)
				return cloneUser(user), nil
			}
		}
	}

	return nil, errors.New("invalid API key")
}

// ValidateSession checks if a session token is valid.
func (m *Manager) ValidateSession(token string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[token]
	if !exists {
		return nil, errors.New("invalid session")
	}

	if time.Now().After(session.ExpiresAt) {
		return nil, errors.New("session expired")
	}

	return session, nil
}

// Logout invalidates a session.
func (m *Manager) Logout(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
	m.saveSessions()
}

// HasPermission checks if a user has a specific permission.
func (m *Manager) HasPermission(role Role, perm Permission) bool {
	perms, exists := rolePermissions[role]
	if !exists {
		return false
	}

	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

// CanManageDomain checks if a user can manage a specific domain.
func (m *Manager) CanManageDomain(user *User, domain string) bool {
	if user.Role == RoleAdmin {
		return true
	}

	// Resellers can only manage their assigned domains
	for _, d := range user.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// GetUser returns a user by username.
func (m *Manager) GetUser(username string) (*User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, exists := m.users[username]
	if !exists {
		return nil, false
	}
	return cloneUser(user), true
}

// GetUserByID returns a user by ID.
func (m *Manager) GetUserByID(id string) (*User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, exists := m.usersByID[id]
	if !exists {
		return nil, false
	}
	return cloneUser(user), true
}

// ListUsers returns all users.
func (m *Manager) ListUsers() []*User {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*User, 0, len(m.users))
	for _, user := range m.users {
		result = append(result, cloneUser(user))
	}
	return result
}

// UpdateUser updates a user.
func (m *Manager) UpdateUser(username string, updates *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[username]
	if !exists {
		return errors.New("user not found")
	}

	if updates.Email != "" {
		user.Email = updates.Email
	}
	if updates.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(updates.Password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}
		user.Password = string(hash)
	}
	if updates.Role != "" && isValidRole(updates.Role) {
		user.Role = updates.Role
	}
	if updates.Domains != nil {
		user.Domains = updates.Domains
	}
	if updates.EnabledSet {
		user.Enabled = updates.Enabled
	}

	user.UpdatedAt = time.Now()
	m.saveUsers()

	// MEDIUM-9: invalidate sessions on password change or disable
	if updates.Password != "" || (updates.EnabledSet && !updates.Enabled) {
		m.invalidateUserSessionsLocked(user.ID)
	}
	return nil
}

// invalidateUserSessionsLocked removes all sessions for a user.
// Must be called with m.mu held. Persists to disk before returning.
func (m *Manager) invalidateUserSessionsLocked(userID string) {
	changed := false
	for token, session := range m.sessions {
		if session.UserID == userID {
			delete(m.sessions, token)
			changed = true
		}
	}
	if changed {
		m.saveSessionsLocked()
	}
}

func cloneUser(user *User) *User {
	if user == nil {
		return nil
	}
	// Build the snapshot field-by-field rather than via struct copy because
	// User now contains sync/atomic.Int64 which go vet flags as noCopy.
	// The cloned LastLogin reflects the latest atomic value so observers see
	// in-process Authenticate updates that haven't been persisted yet.
	copyUser := &User{
		ID:         user.ID,
		Username:   user.Username,
		Email:      user.Email,
		Password:   user.Password,
		Role:       user.Role,
		APIKey:     user.APIKey,
		APIKeyHash: user.APIKeyHash,
		FullAPIKey: user.FullAPIKey,
		Enabled:    user.Enabled,
		CreatedAt:  user.CreatedAt,
		UpdatedAt:  user.UpdatedAt,
		LastLogin:  user.latestLastLogin(),
	}
	if user.Domains != nil {
		copyUser.Domains = append([]string(nil), user.Domains...)
	}
	return copyUser
}

// DeleteUser deletes a user.
func (m *Manager) DeleteUser(username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[username]
	if !exists {
		return errors.New("user not found")
	}

	delete(m.users, username)
	delete(m.usersByID, user.ID)
	if user.APIKeyHash != "" {
		delete(m.usersByAPIKeyHash, user.APIKeyHash)
	}
	m.saveUsers()
	m.invalidateUserSessionsLocked(user.ID)
	return nil
}

// RegenerateAPIKey generates a new API key for a user.
// Returns the full key (shown once to the caller).
func (m *Manager) RegenerateAPIKey(username string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[username]
	if !exists {
		return "", errors.New("user not found")
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		return "", fmt.Errorf("generate API key: %w", err)
	}
	apiKeyPrefix := apiKey
	if len(apiKeyPrefix) > 8 {
		apiKeyPrefix = apiKeyPrefix[:8]
	}

	// Rotate the secondary index entry: drop the old hash mapping (if any)
	// before installing the new one so a regenerated key cannot collide
	// with a stale entry left over from this user.
	if user.APIKeyHash != "" {
		delete(m.usersByAPIKeyHash, user.APIKeyHash)
	}
	user.APIKey = apiKeyPrefix
	user.APIKeyHash = hashAPIKey(apiKey)
	m.usersByAPIKeyHash[user.APIKeyHash] = user
	user.UpdatedAt = time.Now()
	m.saveUsers()

	return apiKey, nil
}

// ChangePassword changes a user's password (requires current password).
func (m *Manager) ChangePassword(username, currentPassword, newPassword string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[username]
	if !exists {
		return errors.New("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)); err != nil {
		return errors.New("invalid current password")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	user.Password = string(hash)
	user.UpdatedAt = time.Now()
	m.saveUsers()
	m.invalidateUserSessionsLocked(user.ID)
	return nil
}

// CleanupSessions removes expired sessions.
func (m *Manager) CleanupSessions() {
	m.mu.Lock()
	changed := false
	now := time.Now()
	for token, session := range m.sessions {
		if now.After(session.ExpiresAt) {
			delete(m.sessions, token)
			changed = true
		}
	}
	m.mu.Unlock()
	if changed {
		m.saveSessions()
	}
}

// loadUsers loads users from disk.
func (m *Manager) loadUsers() {
	file := m.usersFile()
	if file == "" {
		return
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return
	}

	var users []*User
	if err := json.Unmarshal(data, &users); err != nil {
		return
	}

	for _, user := range users {
		m.users[user.Username] = user
		m.usersByID[user.ID] = user
		if user.APIKeyHash != "" {
			m.usersByAPIKeyHash[user.APIKeyHash] = user
		}
	}
}

// saveUsers persists users to disk.
func (m *Manager) saveUsers() {
	file := m.usersFile()
	if file == "" {
		return
	}

	// Ensure directory exists (0700: contains credential files)
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}

	users := make([]*User, 0, len(m.users))
	for _, user := range m.users {
		// Sync live atomic LastLogin into the persisted field so the on-disk
		// JSON reflects in-process authentications.
		if n := user.lastLoginNanos.Load(); n != 0 {
			user.LastLogin = time.Unix(0, n)
		}
		users = append(users, user)
	}

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(file, data, 0600)
}

func (m *Manager) usersFile() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "users.json")
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to urandom — only happens on severe OS entropy failure
		f, fErr := os.Open("/dev/urandom")
		if fErr != nil {
			return "", fmt.Errorf("generate ID: %w", fErr)
		}
		defer f.Close()
		if _, fErr := io.ReadFull(f, b); fErr != nil {
			return "", fmt.Errorf("generate ID fallback: %w", fErr)
		}
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		f, fErr := os.Open("/dev/urandom")
		if fErr != nil {
			return "", fmt.Errorf("generate token: %w", fErr)
		}
		defer f.Close()
		if _, fErr := io.ReadFull(f, b); fErr != nil {
			return "", fmt.Errorf("generate token fallback: %w", fErr)
		}
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		f, fErr := os.Open("/dev/urandom")
		if fErr != nil {
			return "", fmt.Errorf("generate API key: %w", fErr)
		}
		defer f.Close()
		if _, fErr := io.ReadFull(f, b); fErr != nil {
			return "", fmt.Errorf("generate API key fallback: %w", fErr)
		}
	}
	return "uk_" + base64.URLEncoding.EncodeToString(b), nil
}

// hashAPIKey returns the hex-encoded SHA256 hash of an API key.
func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func isValidRole(role Role) bool {
	return role == RoleAdmin || role == RoleReseller || role == RoleUser
}

func isValidUsername(username string) bool {
	if len(username) < 3 || len(username) > 32 {
		return false
	}
	for _, c := range username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Middleware creates an HTTP middleware for authentication.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for public endpoints
		if isPublicEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		var user *User

		// Try API key first (Authorization: Bearer <key>)
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			key := strings.TrimPrefix(auth, "Bearer ")
			if u, err := m.AuthenticateAPIKey(key); err == nil {
				user = u
			}
		}

		// Try session token (X-Session-Token)
		if user == nil {
			if token := r.Header.Get("X-Session-Token"); token != "" {
				if session, err := m.ValidateSession(token); err == nil {
					if u, exists := m.GetUserByID(session.UserID); exists {
						user = u
					}
				}
			}
		}

		if user == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		// Store user in context
		ctx := WithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isPublicEndpoint returns true for endpoints that don't require authentication.
func isPublicEndpoint(path string) bool {
	public := []string{
		"/api/v1/health",
		"/api/v1/auth/login",
		"/_uwas/dashboard",
	}
	for _, p := range public {
		if hasPathPrefixBoundary(path, p) {
			return true
		}
	}
	return false
}

func hasPathPrefixBoundary(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix)
	}
	return strings.HasPrefix(path, prefix+"/")
}

// contextKey is a private type for context keys.
type contextKey int

const userContextKey contextKey = iota

// WithUser adds a user to the context.
func WithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext retrieves the user from the context.
func UserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey).(*User)
	return user, ok
}
