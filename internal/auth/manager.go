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
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	Domains    []string  `json:"domains,omitempty"` // For resellers: managed domains
	APIKey      string    `json:"api_key,omitempty"`      // Display prefix only (first 8 chars)
	APIKeyHash  string    `json:"api_key_hash,omitempty"` // SHA256 hash of full API key
	FullAPIKey  string    `json:"-"`                      // Full key, set only at generation time (not persisted)
	Enabled     bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastLogin  time.Time `json:"last_login,omitempty"`
	EnabledSet bool      `json:"-"`
}

// Session represents an authenticated session.
type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username   string    `json:"username"`
	Role       Role      `json:"role"`
	Domains    []string  `json:"domains,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastStep   int64     `json:"last_step,omitempty"` // TOTP step (Unix/30) last used — prevents replay
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
	users     map[string]*User    // key: username
	usersByID map[string]*User    // key: user ID
	sessions  map[string]*Session // key: token
	dataDir       string
	apiKey        string // Global admin API key (backward compat)
	jwtSecret     []byte

	// Brute-force protection: tracks failed login attempts per username.
	loginAttemptsMu sync.Mutex
	loginAttempts   map[string][]time.Time // username -> timestamps of failed attempts

	// Background session pruner. Closed by Stop(). Nil when sessionCleanupInterval
	// is 0 (e.g. tests that want full control).
	cleanupDone chan struct{}
}

// sessionCleanupInterval is how often the background goroutine sweeps the
// session map for expired entries. One hour is well below the 24h session
// lifetime, so the worst-case leak is ~1h of expired sessions in memory.
const sessionCleanupInterval = 1 * time.Hour

// NewManager creates a new auth manager.
func NewManager(dataDir, globalAPIKey string) *Manager {
	m := &Manager{
		users:         make(map[string]*User),
		usersByID:     make(map[string]*User),
		sessions:      make(map[string]*Session),
		loginAttempts: make(map[string][]time.Time),
		dataDir:       dataDir,
		apiKey:        globalAPIKey,
		cleanupDone:   make(chan struct{}),
	}
	if err := m.loadOrCreateJWTSecret(); err != nil {
		panic("auth: jwt secret init failed: " + err.Error())
	}
	m.loadUsers()
	m.loadSessions()
	go m.sessionCleanupLoop()
	return m
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
)

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

	if _, exists := m.users[username]; exists {
		return nil, errors.New("user already exists")
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// Generate API key
	apiKey := generateAPIKey()
	apiKeyPrefix := apiKey
	if len(apiKeyPrefix) > 8 {
		apiKeyPrefix = apiKeyPrefix[:8]
	}

	user := &User{
		ID:         generateID(),
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
	m.saveUsers()

	return cloneUser(user), nil
}

// Authenticate validates credentials and returns a session.
func (m *Manager) Authenticate(username, password string) (*Session, error) {
	if m.isLockedOut(username) {
		return nil, errors.New("too many failed attempts; try again later")
	}

	m.mu.RLock()
	user, exists := m.users[username]
	if !exists {
		m.mu.RUnlock()
		m.recordFailedAttempt(username)
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
		m.recordFailedAttempt(username)
		return nil, errors.New("user disabled")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		m.recordFailedAttempt(username)
		return nil, errors.New("invalid credentials")
	}

	m.clearFailedAttempts(username)

	// Update last login
	m.mu.Lock()
	user.LastLogin = time.Now()
	m.mu.Unlock()

	session := &Session{
		Token:     generateToken(),
		UserID:    userID,
		Username:  username,
		Role:      role,
		Domains:   domains,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
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

	for _, user := range m.users {
		if !user.Enabled {
			continue
		}

		// Prefer hash-based comparison (current scheme)
		if user.APIKeyHash != "" {
			if subtle.ConstantTimeCompare([]byte(keyHash), []byte(user.APIKeyHash)) == 1 {
				return cloneUser(user), nil
			}
			continue
		}

		// Backward compatibility: legacy plaintext key (no hash stored yet)
		if user.APIKey != "" {
			if subtle.ConstantTimeCompare([]byte(key), []byte(user.APIKey)) == 1 {
				slog.Warn("API key authenticated via legacy plaintext comparison; user should regenerate their key",
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
	copyUser := *user
	if user.Domains != nil {
		copyUser.Domains = append([]string(nil), user.Domains...)
	}
	copyUser.EnabledSet = false
	return &copyUser
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

	apiKey := generateAPIKey()
	apiKeyPrefix := apiKey
	if len(apiKeyPrefix) > 8 {
		apiKeyPrefix = apiKeyPrefix[:8]
	}

	user.APIKey = apiKeyPrefix
	user.APIKeyHash = hashAPIKey(apiKey)
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

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)
}

func generateAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "uk_" + base64.URLEncoding.EncodeToString(b)
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
