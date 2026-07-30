// Package authmw contains the admin API authentication middleware,
// extracted from the admin package to reduce the api.go god-object.
//
// The middleware is constructed with a Deps struct of function closures
// to avoid a circular dependency: authmw imports auth (for User/Session
// types) but never imports admin; admin imports authmw and wires the
// closures to its Server methods at construction time.
package authmw

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/respond"
)

// CtxPinVerified is the context key set when a single-use ticket was
// minted with a PIN. Handlers check it via r.Context().Value(CtxPinVerified).
const CtxPinVerified ctxKey = "pinVerified"

type ctxKey string

// Deps holds the dependencies the middleware needs to authenticate
// requests and enforce rate limits / CSRF / 2FA. Each field is a closure
// so the calling Server retains ownership of its config mutex, auth
// manager, and rate-limit state.
type Deps struct {
	// Config accessors (read per-request so config changes take effect
	// without restart).
	GetAPIKey       func() string
	GetUsersEnabled func() bool
	GetTOTPSecret   func() string

	// Multi-user auth (nil when multi-user mode is disabled).
	AuthByKey    func(key string) (*auth.User, error)
	ValidateSess func(token string) (*auth.Session, error)
	GetUserByID  func(id string) (*auth.User, bool)

	// Rate limiting and ticket redemption.
	CheckRateLimit func(ip string) bool
	RecordFailure  func(ip string)
	RedeemTicket   func(ticket string) (token string, pinOK bool)

	// TOTP validation (nil when TOTP is not configured).
	ValidateTOTP func(secret, code string) (bool, error)
}

// New returns an http.Handler that wraps `next` with the full auth chain:
//
//   - CORS preflight handling
//   - Public endpoint bypass
//   - Login rate limiting
//   - Global IP rate limiting
//   - Multi-user API key / session / ticket auth
//   - Legacy API key auth (timing-safe)
//   - TOTP 2FA gate
//   - CSRF origin check
func New(deps Deps, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := deps.GetAPIKey()
		multiUserEnabled := deps.GetUsersEnabled()

		// If no auth configured at all, allow all (inject virtual admin for compat)
		if apiKey == "" && !multiUserEnabled {
			user := &auth.User{ID: "local", Username: "admin", Role: auth.RoleAdmin, Enabled: true}
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
			return
		}

		// CORS: only allow the dashboard's own origin (or localhost for dev).
		if origin := r.Header.Get("Origin"); origin != "" {
			if IsAllowedOrigin(origin, r) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Session-Token, X-TOTP-Code, X-Pin-Code")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Add("Vary", "Origin")
			}
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		// Public endpoints: health check, dashboard UI, read-only branding,
		// deploy webhooks (no auth needed).
		if r.URL.Path == "/api/v1/health" ||
			(r.URL.Path == "/api/v1/settings/branding" && r.Method == "GET") ||
			strings.HasPrefix(r.URL.Path, "/_uwas/dashboard") ||
			(strings.HasPrefix(r.URL.Path, "/api/v1/apps/") && strings.HasSuffix(r.URL.Path, "/webhook") && r.Method == "POST") {
			next.ServeHTTP(w, r)
			return
		}

		// Login endpoint: public but rate-limited.
		if r.URL.Path == "/api/v1/auth/login" || (r.URL.Path == "/api/v1/auth/bootstrap" && r.Method == "POST") {
			ip := RequestIP(r)
			if deps.CheckRateLimit(ip) {
				w.Header().Set("Retry-After", "300")
				respond.Error(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Rate limiting: check if IP is blocked before auth check.
		ip := RequestIP(r)
		if deps.CheckRateLimit(ip) {
			w.Header().Set("Retry-After", "300")
			respond.Error(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
			return
		}

		var authenticated bool
		var user *auth.User
		var ticketPinVerified bool

		// Try multi-user auth first if enabled.
		if multiUserEnabled && deps.AuthByKey != nil {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				key := strings.TrimPrefix(authHeader, "Bearer ")
				if u, err := deps.AuthByKey(key); err == nil {
					authenticated = true
					user = u
				}
			}

			// Try session token (X-Session-Token header).
			if !authenticated {
				if token := r.Header.Get("X-Session-Token"); token != "" {
					if session, err := deps.ValidateSess(token); err == nil {
						if u, exists := deps.GetUserByID(session.UserID); exists {
							authenticated = true
							user = u
						}
					}
				}
			}

			// Check ticket query param for SSE/WebSocket (short-lived, single-use).
			if !authenticated {
				if ticket := r.URL.Query().Get("ticket"); ticket != "" {
					realToken, pinOK := deps.RedeemTicket(ticket)
					if realToken != "" {
						if session, err := deps.ValidateSess(realToken); err == nil {
							if u, exists := deps.GetUserByID(session.UserID); exists {
								authenticated = true
								user = u
							}
						}
						if !authenticated {
							if u, err := deps.AuthByKey(realToken); err == nil {
								authenticated = true
								user = u
							}
						}
					}
					if authenticated {
						ticketPinVerified = pinOK
						q := r.URL.Query()
						q.Del("ticket")
						r.URL.RawQuery = q.Encode()
					}
				}
			}
		}

		// Fall back to legacy API key auth if multi-user auth failed or not enabled.
		if !authenticated && apiKey != "" {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				if ticket := r.URL.Query().Get("ticket"); ticket != "" {
					if realToken, pinOK := deps.RedeemTicket(ticket); realToken != "" {
						authHeader = "Bearer " + realToken
						ticketPinVerified = pinOK
					}
				}
			}
			if subtle.ConstantTimeCompare([]byte(authHeader), []byte("Bearer "+apiKey)) == 1 {
				authenticated = true
				user = &auth.User{
					ID:       "admin",
					Username: "admin",
					Role:     auth.RoleAdmin,
					Enabled:  true,
				}
			}
		}

		if !authenticated {
			deps.RecordFailure(ip)
			respond.Error(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// Store user in context for handlers to access.
		ctx := auth.WithUser(r.Context(), user)
		if ticketPinVerified {
			ctx = context.WithValue(ctx, CtxPinVerified, true)
		}
		r = r.WithContext(ctx)

		// 2FA check for legacy auth: if TOTP is enabled, require valid code.
		totpSecret := deps.GetTOTPSecret()
		if totpSecret != "" && user.Role == auth.RoleAdmin &&
			!strings.HasPrefix(r.URL.Path, "/api/v1/auth/2fa/") {
			totpCode := r.Header.Get("X-TOTP-Code")
			if totpCode == "" {
				w.Header().Set("X-2FA-Required", "true")
				respond.Error(w, http.StatusForbidden, "2fa_required")
				return
			}
			valid, _ := deps.ValidateTOTP(totpSecret, totpCode)
			if !valid {
				deps.RecordFailure(ip)
				respond.Error(w, http.StatusForbidden, "invalid 2FA code")
				return
			}
		}

		// CSRF protection: state-changing methods must send X-Requested-With
		// or come from the dashboard origin. Also applied to expensive GET
		// endpoints (database export, config export) that could be abused
		// for DoS via CSRF.
		needsCSRFCheck := r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" || r.Method == "DELETE"
		if !needsCSRFCheck && r.Method == "GET" && IsExpensiveGET(r.URL.Path) {
			needsCSRFCheck = true
		}
		if needsCSRFCheck {
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				origin := r.Header.Get("Origin")
				referer := r.Header.Get("Referer")
				sameOrigin := origin != "" && IsAllowedOrigin(origin, r)
				if !sameOrigin && referer != "" {
					if u, err := url.Parse(referer); err == nil {
						sameOrigin = IsAllowedOrigin(u.Scheme+"://"+u.Host, r)
					}
				}
				if !sameOrigin {
					respond.Error(w, http.StatusForbidden, "csrf: invalid origin")
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

// IsExpensiveGET reports whether a GET endpoint has enough side-effect cost
// (full database dump, full config dump, etc.) that an attacker forcing the
// admin's browser to fetch it via an <img>/<iframe> CSRF would constitute a
// real denial-of-service even though the attacker never reads the response.
func IsExpensiveGET(path string) bool {
	switch {
	case strings.HasSuffix(path, "/export"),
		strings.HasSuffix(path, "/backup"),
		strings.HasSuffix(path, "/download"):
		return true
	}
	return false
}

// IsAllowedOrigin returns true when the Origin header belongs to the
// dashboard itself (same scheme+host as the admin listener) or is a
// localhost address (for local development).
func IsAllowedOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}

	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	dashboardOrigin := scheme + "://" + r.Host
	return origin == dashboardOrigin
}

// RequestIP extracts the client IP from a request, stripping the port.
func RequestIP(r *http.Request) string {
	host, _, err := splitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// splitHostPort is net.SplitHostPort inlined to avoid importing net.
func splitHostPort(addr string) (host, port string, err error) {
	// IPv6 bracketed
	if strings.HasPrefix(addr, "[") {
		end := strings.LastIndex(addr, "]")
		if end < 0 {
			return addr, "", nil
		}
		host = addr[1:end]
		rest := addr[end+1:]
		if strings.HasPrefix(rest, ":") {
			return host, rest[1:], nil
		}
		return host, "", nil
	}
	// Plain host:port
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr, "", nil
	}
	return addr[:idx], addr[idx+1:], nil
}
