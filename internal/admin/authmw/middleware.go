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
	"net"
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

	// RedeemTicketEx, when set, is used instead of RedeemTicket and also reports
	// whether the ticket vouches for the second factor. An EventSource or
	// WebSocket request cannot carry X-TOTP-Code; the ticket it redeems was
	// minted by a request that did (F37000).
	RedeemTicketEx func(ticket string) (token string, pinOK, totpOK bool)

	// TOTP validation (nil when TOTP is not configured). It runs on every
	// request of an admin, and clients send the same code on each of them for
	// as long as it is valid, so it must not consume the matched time step;
	// single-use enforcement belongs to the one-time 2FA operations.
	ValidateTOTP func(secret, code string) bool
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
	redeem := func(ticket string) (token string, pinOK, totpOK bool) {
		if deps.RedeemTicketEx != nil {
			return deps.RedeemTicketEx(ticket)
		}
		token, pinOK = deps.RedeemTicket(ticket)
		return token, pinOK, false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := deps.GetAPIKey()
		multiUserEnabled := deps.GetUsersEnabled()

		// If no auth configured at all, allow all (inject virtual admin for compat)
		if apiKey == "" && !multiUserEnabled {
			// The listener is loopback-only in this mode, but a browser on the
			// same machine still reaches it for any web page, so the request
			// must be vetted as if a page had sent it (F15800). Without this,
			// a page the operator merely visits can POST to the admin API, and
			// a DNS-rebound name can read from it.
			if !IsLoopbackHost(r.Host) {
				respond.Error(w, http.StatusForbidden,
					"admin API without credentials answers to loopback host names only; set global.admin.api_key")
				return
			}
			// The Host header is chosen by the client, so it cannot stand for
			// "the machine's own browser". Start refuses to bind this mode off
			// loopback, but the credentials can be cleared while the listener
			// runs (a settings PUT, a config edit and reload); the peer address
			// is the only thing that still says who is asking (F16101).
			if !IsLoopbackPeer(r.RemoteAddr) {
				respond.Error(w, http.StatusForbidden,
					"admin API without credentials answers to loopback clients only; set global.admin.api_key")
				return
			}
			if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" || r.Method == "DELETE" ||
				(r.Method == "GET" && IsExpensiveGET(r.URL.Path)) {
				// Browsers attach Origin to every cross-origin write; a client
				// that sends none (curl, scripts) is not a page.
				origin := r.Header.Get("Origin")
				if origin != "" && !IsAllowedOrigin(origin, r) {
					respond.Error(w, http.StatusForbidden, "csrf: invalid origin")
					return
				}
				// A sub-resource GET (<img>, <script>, a prefetch) carries no
				// Origin, so the expensive-GET case above never saw a page's
				// request. Browsers label every such request Sec-Fetch-Site and
				// a curl-style client sends none (F45000).
				if origin == "" && strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
					respond.Error(w, http.StatusForbidden, "csrf: cross-site request")
					return
				}
			}
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
		// Tickets are single-use: once redeemed here, the legacy fallback must
		// reuse the result instead of redeeming (and failing) a second time.
		var ticketRedeemed bool
		var ticketToken string
		var ticketPinOK bool
		var ticketTOTPOK bool
		// Set once the request authenticated through a ticket whose mint request
		// passed the 2FA gate.
		var ticketTOTPVerified bool

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
					realToken, pinOK, totpOK := redeem(ticket)
					ticketRedeemed, ticketToken, ticketPinOK, ticketTOTPOK = true, realToken, pinOK, totpOK
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
						ticketTOTPVerified = totpOK
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
					if !ticketRedeemed {
						ticketToken, ticketPinOK, ticketTOTPOK = redeem(ticket)
					}
					if ticketToken != "" {
						authHeader = "Bearer " + ticketToken
						ticketPinVerified = ticketPinOK
					}
				}
			}
			if subtle.ConstantTimeCompare([]byte(authHeader), []byte("Bearer "+apiKey)) == 1 {
				authenticated = true
				ticketTOTPVerified = ticketTOTPOK && r.Header.Get("Authorization") == ""
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
			switch {
			case totpCode == "" && ticketTOTPVerified:
				// The ticket was minted by a request that passed this gate.
			case totpCode == "":
				w.Header().Set("X-2FA-Required", "true")
				respond.Error(w, http.StatusForbidden, "2fa_required")
				return
			default:
				if !deps.ValidateTOTP(totpSecret, totpCode) {
					deps.RecordFailure(ip)
					respond.Error(w, http.StatusForbidden, "invalid 2FA code")
					return
				}
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

	// Exact hostname match only — "localhost.evil.com" must NOT match "localhost".
	// Use == instead of strings.HasPrefix to prevent subdomain bypass.

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	dashboardOrigin := scheme + "://" + r.Host
	return isSameOrigin(u, dashboardOrigin)
}

// isSameOrigin returns true when parsed URL u has the same scheme+host
// as targetOrigin. It correctly handles the case where the browser omits
// the default port (e.g. Origin: https://admin.example.com vs r.Host
// admin.example.com:443) by comparing hosts and normalising ports.
func isSameOrigin(u *url.URL, targetOrigin string) bool {
	tu, err := url.Parse(targetOrigin)
	if err != nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, tu.Scheme) {
		return false
	}
	if !strings.EqualFold(u.Hostname(), tu.Hostname()) {
		return false
	}
	return defaultPort(u.Scheme, u.Port()) == defaultPort(tu.Scheme, tu.Port())
}

// defaultPort returns the explicit port, or the canonical port for the scheme.
func defaultPort(scheme, explicitPort string) string {
	if explicitPort != "" {
		return explicitPort
	}
	switch strings.ToLower(scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// IsLoopbackHost reports whether a Host header names the local machine:
// "localhost" (or a name under it) or a loopback IP literal, with or without a
// port. Anything else - a name an attacker controls and can point at 127.0.0.1
// - is not.
func IsLoopbackHost(hostport string) bool {
	host, _, _ := splitHostPort(strings.TrimSpace(hostport))
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsLoopbackPeer reports whether a connection's remote address ("ip:port") is a
// loopback address. An address it cannot parse is not one.
func IsLoopbackPeer(remoteAddr string) bool {
	host, _, _ := splitHostPort(strings.TrimSpace(remoteAddr))
	if i := strings.IndexByte(host, '%'); i >= 0 { // IPv6 zone
		host = host[:i]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
