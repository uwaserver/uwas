package admin

import (
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/serverip"
	"github.com/uwaserver/uwas/internal/siteuser"
	"github.com/uwaserver/uwas/internal/webhook"
)

type authTicket struct {
	token     string    // the real session token or API key
	created   time.Time // when the ticket was issued
	expiresAt time.Time // when the ticket expires
}

// handleAuthTicket issues a short-lived, single-use ticket that can be passed
// as a query parameter for SSE/WebSocket connections. This avoids putting the
// real token in the URL (which leaks into logs, Referer, browser history).
func (s *Server) handleAuthTicket(w http.ResponseWriter, r *http.Request) {
	// The caller is already authenticated (middleware ran). Extract whichever
	// token they presented: Authorization: Bearer for API keys, or
	// X-Session-Token for browser sessions. Both are accepted by the auth
	// middleware and by redeemTicket, so accepting both here keeps session
	// users from falling back to passing the raw token in the query string —
	// which is exactly the leak the ticket system exists to prevent.
	var realToken string
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		if t := strings.TrimPrefix(authHeader, "Bearer "); t != "" && t != authHeader {
			realToken = t
		}
	}
	if realToken == "" {
		realToken = r.Header.Get("X-Session-Token")
	}
	if realToken == "" {
		jsonError(w, "bearer token or session token required", http.StatusBadRequest)
		return
	}

	b := make([]byte, 20)
	if _, err := crand.Read(b); err != nil {
		jsonError(w, "entropy failure", http.StatusInternalServerError)
		return
	}
	ticket := hex.EncodeToString(b)

	const ticketTTL = 30 * time.Second

	s.ticketMu.Lock()
	if s.tickets == nil {
		s.tickets = make(map[string]*authTicket)
	}
	// Prune expired tickets.
	now := time.Now()
	for k, t := range s.tickets {
		if now.After(t.expiresAt) {
			delete(s.tickets, k)
		}
	}
	s.tickets[ticket] = &authTicket{token: realToken, created: now, expiresAt: now.Add(ticketTTL)}
	s.ticketMu.Unlock()

	jsonResponse(w, map[string]any{"ticket": ticket, "expires_at": now.Add(ticketTTL)})
}

// redeemTicket exchanges a single-use ticket for the real auth token.
// Returns empty string if the ticket is invalid or expired.
// Uses atomic delete — single-use: once redeemed, the ticket is deleted.
func (s *Server) redeemTicket(ticket string) string {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	t, ok := s.tickets[ticket]
	if !ok {
		return ""
	}
	if time.Now().After(t.expiresAt) {
		delete(s.tickets, ticket)
		return ""
	}
	// Single-use: delete now so it cannot be redeemed again, then return the token.
	delete(s.tickets, ticket)
	return t.token
}

func (s *Server) requireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool {
	if s.canAccessDomain(r, domain) {
		return true
	}
	if action != "" {
		s.recordAuditR(r, action, "domain: "+domain+" (forbidden)", false)
	}
	jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
	return false
}

func (s *Server) canAccessDomain(r *http.Request, domain string) bool {
	if s.authMgr == nil {
		return true
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user.Role == auth.RoleAdmin {
		return true
	}
	return s.authMgr.CanManageDomain(user, domain)
}

type adminUserResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      auth.Role `json:"role"`
	Domains   []string  `json:"domains,omitempty"`
	APIKey    string    `json:"api_key,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LastLogin time.Time `json:"last_login,omitempty"`
}

func adminUserDTO(user *auth.User, revealAPIKey bool) adminUserResponse {
	apiKey := maskSecret(user.APIKey)
	if revealAPIKey && user.FullAPIKey != "" {
		apiKey = user.FullAPIKey
	} else if revealAPIKey {
		apiKey = user.APIKey
	}
	return adminUserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Role:      user.Role,
		Domains:   append([]string(nil), user.Domains...),
		APIKey:    apiKey,
		Enabled:   user.Enabled,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
		LastLogin: user.LastLogin,
	}
}

// isAllowedOrigin returns true when the Origin header belongs to the
// dashboard itself (same scheme+host as the admin listener) or is a
// localhost address (for local development).
func isAllowedOrigin(origin string, r *http.Request) bool {
	// Allow any localhost origin for dev convenience.
	lower := strings.ToLower(origin)
	if strings.HasPrefix(lower, "http://localhost") ||
		strings.HasPrefix(lower, "https://localhost") ||
		strings.HasPrefix(lower, "http://127.0.0.1") ||
		strings.HasPrefix(lower, "https://127.0.0.1") {
		return true
	}

	// Allow the dashboard's own origin: derive it from the Host header
	// which is the admin listener itself.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	dashboardOrigin := scheme + "://" + r.Host
	return origin == dashboardOrigin
}

// --- SFTP Users ---

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	users := siteuser.ListUsers()
	if users == nil {
		users = []siteuser.User{}
	}
	limit, offset := parsePagination(r)
	users, total := paginateSlice(users, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  users,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}

	root, err := s.siteUserRoot(req.Domain)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if root == "" {
		jsonError(w, "domain root not found", http.StatusNotFound)
		return
	}

	identity := req.Domain
	if appName, ok := appSFTPTargetName(req.Domain); ok {
		identity = appSFTPIdentity(appName)
	}
	user, password, err := siteuser.CreateUserForWebDir(root, identity)
	if err != nil {
		jsonError(w, "create user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.logger.Info("SFTP user created", "target", req.Domain, "identity", identity, "username", user.Username)
	jsonResponse(w, map[string]string{
		"username":  user.Username,
		"domain":    identity,
		"password":  password,
		"home_dir":  user.HomeDir,
		"web_dir":   user.WebDir,
		"server_ip": serverip.PublicIP(),
		"port":      "22",
	})
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	identity := domain
	if appName, ok := appSFTPTargetName(domain); ok {
		if !s.requireAdmin(w, r) {
			s.recordAuditR(r, "sftp.delete", "app: "+domain+" (forbidden)", false)
			return
		}
		identity = appSFTPIdentity(appName)
	} else {
		if !s.requireDomainAccess(w, r, domain, "sftp.delete") {
			return
		}
	}
	if !s.requirePin(w, r) {
		return
	}
	if err := siteuser.DeleteUser(identity); err != nil {
		jsonError(w, "delete user: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("SFTP user deleted", "target", domain, "identity", identity)
	jsonResponse(w, map[string]string{"status": "deleted", "domain": identity})
}

func (s *Server) ensureAuthManagerFromConfig() {
	s.configMu.RLock()
	enabled := s.config.Global.Users.Enabled
	webRoot := s.config.Global.WebRoot
	apiKey := s.config.Global.Admin.APIKey
	allowLegacyPlaintext := s.config.Global.Users.AllowLegacyPlaintextAPIKey
	s.configMu.RUnlock()

	if !enabled {
		return
	}
	if s.authMgr == nil {
		mgr := auth.NewManager(webRoot, apiKey)
		mgr.SetAllowLegacyPlaintextKey(allowLegacyPlaintext)
		s.authMgr = mgr
		if s.logger != nil {
			s.logger.Info("multi-user auth enabled from settings")
		}
		return
	}
	if mgr, ok := s.authMgr.(*auth.Manager); ok {
		mgr.SetAllowLegacyPlaintextKey(allowLegacyPlaintext)
	}
}

// ── 2FA / TOTP ──────────────────────────────────────────────────────────────

func (s *Server) handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	enabled := s.config.Global.Admin.TOTPSecret != ""
	s.configMu.RUnlock()
	jsonResponse(w, map[string]bool{"enabled": enabled})
}

func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	existing := s.config.Global.Admin.TOTPSecret
	s.configMu.RUnlock()
	if existing != "" {
		jsonError(w, "2FA is already enabled; disable it first to reconfigure", http.StatusConflict)
		return
	}

	secret, err := GenerateTOTPSecret()
	if err != nil {
		jsonError(w, "failed to generate secret", http.StatusInternalServerError)
		return
	}

	uri := TOTPProvisioningURI(secret, "admin", "UWAS")
	// Don't save yet — user must verify with a valid code first.
	// Store per-user so concurrent 2FA setups don't overwrite each other.
	username := "admin"
	if user, ok := auth.UserFromContext(r.Context()); ok {
		username = user.Username
	}
	s.pendingTOTPMu.Lock()
	if s.pendingTOTP == nil {
		s.pendingTOTP = make(map[string]string)
	}
	s.pendingTOTP[username] = secret
	s.pendingTOTPMu.Unlock()

	jsonResponse(w, map[string]string{
		"secret": secret,
		"uri":    uri,
	})
}

func (s *Server) handle2FAVerify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	username := "admin"
	if user, ok := auth.UserFromContext(r.Context()); ok {
		username = user.Username
	}

	s.pendingTOTPMu.Lock()
	secret := ""
	if s.pendingTOTP != nil {
		secret = s.pendingTOTP[username]
	}
	s.pendingTOTPMu.Unlock()

	if secret == "" {
		// Already enabled — validate against active secret
		s.configMu.RLock()
		secret = s.config.Global.Admin.TOTPSecret
		s.configMu.RUnlock()
	}

	if secret == "" {
		jsonError(w, "no 2FA setup pending; call /auth/2fa/setup first", http.StatusBadRequest)
		return
	}

	valid, _ := ValidateTOTP(secret, req.Code)
	if !valid {
		jsonError(w, "invalid code", http.StatusUnauthorized)
		return
	}

	// If this was a pending setup, activate it.
	s.pendingTOTPMu.Lock()
	pending := ""
	if s.pendingTOTP != nil {
		pending = s.pendingTOTP[username]
		delete(s.pendingTOTP, username)
	}
	s.pendingTOTPMu.Unlock()

	if pending != "" {
		s.configMu.Lock()
		s.config.Global.Admin.TOTPSecret = pending
		s.configMu.Unlock()
	}

	s.persistConfig()
	s.recordAuditR(r, "2fa.enabled", "TOTP activated", true)

	jsonResponse(w, map[string]any{"status": "2fa_enabled"})
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.configMu.RLock()
	secret := s.config.Global.Admin.TOTPSecret
	s.configMu.RUnlock()

	if secret == "" {
		jsonError(w, "2FA is not enabled", http.StatusBadRequest)
		return
	}

	valid, _ := ValidateTOTP(secret, req.Code)
	if !valid {
		jsonError(w, "invalid code", http.StatusUnauthorized)
		return
	}

	s.configMu.Lock()
	s.config.Global.Admin.TOTPSecret = ""
	s.configMu.Unlock()

	s.persistConfig()
	s.recordAuditR(r, "2fa.disabled", "TOTP deactivated", true)

	jsonResponse(w, map[string]any{"status": "2fa_disabled"})
}

func (s *Server) handleUserChangePasswordAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if currentUser.Role == auth.RoleAdmin {
		updates := &auth.User{Password: req.NewPassword}
		if err := s.authMgr.UpdateUser(username, updates); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		if currentUser.Username != username {
			jsonError(w, "forbidden", http.StatusForbidden)
			return
		}
		if req.CurrentPassword == "" {
			jsonError(w, "current_password required", http.StatusBadRequest)
			return
		}
		if err := s.authMgr.ChangePassword(username, req.CurrentPassword, req.NewPassword); err != nil {
			jsonError(w, err.Error(), http.StatusUnauthorized)
			return
		}
	}

	s.recordAuditR(r, "auth.user.password", username, true)

	jsonResponse(w, map[string]string{"status": "password_changed"})
}

// requirePin checks the X-Pin-Code header against the configured pin_code.
// Returns true if pin is valid or no pin is configured. Returns false and
// sends 403 if pin is required but missing/wrong.
func (s *Server) requirePin(w http.ResponseWriter, r *http.Request) bool {
	s.configMu.RLock()
	pin := s.config.Global.Admin.PinCode
	s.configMu.RUnlock()

	if pin == "" {
		return true // no pin configured, allow
	}

	provided := r.Header.Get("X-Pin-Code")
	// WebSocket connections can't set headers — also check query param
	if provided == "" {
		provided = r.URL.Query().Get("pin")
	}
	if provided == "" {
		jsonError(w, "pin_required", http.StatusForbidden)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(pin)) != 1 {
		s.recordAuditR(r, "pin.failed", r.URL.Path, false)
		jsonError(w, "invalid_pin", http.StatusForbidden)
		return false
	}
	return true
}

// requireAdmin checks if the authenticated user has the admin role.

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user.Role != auth.RoleAdmin {
		jsonError(w, "admin required", http.StatusForbidden)
		return false
	}
	return true
}

// ── Multi-User Authentication ───────────────────────────────────────────────

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		jsonError(w, "username and password required", http.StatusBadRequest)
		return
	}

	session, err := s.authMgr.Authenticate(req.Username, req.Password)
	if err != nil {
		ip := requestIP(r)
		s.recordAuthFailure(ip, req.Username)
		if s.webhookMgr != nil {
			s.webhookMgr.Fire(webhook.EventLoginFailed, map[string]any{
				"username": req.Username,
				"ip":       ip,
			})
		}
		jsonError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	ip := requestIP(r)
	// Login is unauthenticated when invoked, but we know who succeeded — pass
	// the username explicitly rather than relying on context (no middleware ran).
	s.RecordAuditUser("auth.login", req.Username, ip, req.Username, true)
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventLoginSuccess, map[string]any{
			"username": req.Username,
			"ip":       ip,
		})
	}

	jsonResponse(w, map[string]any{
		"status":     "authenticated",
		"token":      session.Token,
		"user_id":    session.UserID,
		"username":   session.Username,
		"role":       session.Role,
		"domains":    session.Domains,
		"expires_at": session.ExpiresAt,
	})
}

func (s *Server) handleAuthBootstrap(w http.ResponseWriter, r *http.Request) {
	s.ensureAuthManagerFromConfig()
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	s.configMu.RLock()
	apiKey := s.config.Global.Admin.APIKey
	usersEnabled := s.config.Global.Users.Enabled
	s.configMu.RUnlock()
	if !usersEnabled || apiKey != "" {
		jsonError(w, "bootstrap is not available", http.StatusForbidden)
		return
	}
	if len(s.authMgr.ListUsers()) != 0 {
		jsonError(w, "bootstrap is already complete", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		jsonError(w, "username and password required", http.StatusBadRequest)
		return
	}

	user, err := s.authMgr.CreateUser(req.Username, req.Email, req.Password, auth.RoleAdmin, nil)
	if err != nil {
		ip := requestIP(r)
		s.recordAuthFailure(ip, req.Username)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	session, err := s.authMgr.Authenticate(req.Username, req.Password)
	if err != nil {
		jsonError(w, "bootstrap login failed", http.StatusInternalServerError)
		return
	}

	ip := requestIP(r)
	s.RecordAuditUser("auth.bootstrap", req.Username, ip, req.Username, true)
	jsonResponse(w, map[string]any{
		"status":     "authenticated",
		"token":      session.Token,
		"user_id":    session.UserID,
		"username":   session.Username,
		"role":       session.Role,
		"domains":    session.Domains,
		"expires_at": session.ExpiresAt,
		"api_key":    user.FullAPIKey,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	// Try to get token from header or body
	token := r.Header.Get("X-Session-Token")
	if token == "" {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			token = req.Token
		}
	}

	if token != "" {
		s.authMgr.Logout(token)
	}

	s.recordAuditR(r, "auth.logout", "", true)

	jsonResponse(w, map[string]string{"status": "logged_out"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	jsonResponse(w, adminUserDTO(user, false))
}

func (s *Server) handleUserListAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Only admin can list all users; resellers can only see themselves
	if user.Role != auth.RoleAdmin {
		jsonResponse(w, []adminUserResponse{adminUserDTO(user, false)})
		return
	}

	users := s.authMgr.ListUsers()
	result := make([]adminUserResponse, 0, len(users))
	for _, u := range users {
		result = append(result, adminUserDTO(u, false))
	}

	jsonResponse(w, result)
}

func (s *Server) handleUserGetAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	// Users can only get their own info unless they're admin
	if currentUser.Role != auth.RoleAdmin && currentUser.Username != username {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	user, exists := s.authMgr.GetUser(username)
	if !exists {
		jsonError(w, "user not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, adminUserDTO(user, false))
}

func (s *Server) handleUserCreateAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Username string   `json:"username"`
		Email    string   `json:"email"`
		Password string   `json:"password"`
		Role     string   `json:"role"`
		Domains  []string `json:"domains"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Only admin can create users; resellers can create users only if allowed
	if currentUser.Role != auth.RoleAdmin {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	role := auth.Role(req.Role)
	if role != auth.RoleAdmin && role != auth.RoleUser && role != auth.RoleReseller {
		jsonError(w, "invalid role", http.StatusBadRequest)
		return
	}

	// Check if reseller role is allowed
	s.configMu.RLock()
	allowReseller := s.config.Global.Users.AllowResller
	s.configMu.RUnlock()
	if role == auth.RoleReseller && !allowReseller {
		jsonError(w, "reseller role not allowed", http.StatusBadRequest)
		return
	}

	user, err := s.authMgr.CreateUser(req.Username, req.Email, req.Password, role, req.Domains)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.create", req.Username+" ("+req.Role+")", true)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(adminUserDTO(user, true))
}

func (s *Server) handleUserUpdateAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Email    *string  `json:"email,omitempty"`
		Password *string  `json:"password,omitempty"`
		Enabled  *bool    `json:"enabled,omitempty"`
		Domains  []string `json:"domains,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Users can only update themselves unless they're admin
	if currentUser.Role != auth.RoleAdmin && currentUser.Username != username {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	updates := &auth.User{}
	if req.Email != nil {
		updates.Email = *req.Email
	}
	if req.Password != nil {
		updates.Password = *req.Password
	}
	if req.Enabled != nil {
		updates.Enabled = *req.Enabled
		updates.EnabledSet = true
	}
	if req.Domains != nil {
		// Only admin can change domains
		if currentUser.Role != auth.RoleAdmin {
			jsonError(w, "forbidden: only admin can change domains", http.StatusForbidden)
			return
		}
		updates.Domains = req.Domains
	}

	if err := s.authMgr.UpdateUser(username, updates); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.update", username, true)

	jsonResponse(w, map[string]string{"status": "updated"})
}

func (s *Server) handleUserDeleteAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	// Users cannot delete themselves; only admin can delete other users
	if currentUser.Username == username {
		jsonError(w, "cannot delete yourself", http.StatusBadRequest)
		return
	}
	if currentUser.Role != auth.RoleAdmin {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := s.authMgr.DeleteUser(username); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.delete", username, true)

	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleUserRegenerateAPIKeyAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	// Users can only regenerate their own API key unless they're admin
	if currentUser.Role != auth.RoleAdmin && currentUser.Username != username {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	newKey, err := s.authMgr.RegenerateAPIKey(username)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.apikey", username, true)

	jsonResponse(w, map[string]string{"api_key": newKey})
}
