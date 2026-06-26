# sc-clickjacking results

Summary: Public-facing sites are protected by a global `X-Frame-Options: SAMEORIGIN` middleware and the JSON API sets `X-Frame-Options: DENY`, but the embedded admin dashboard HTML (the SPA shell served at `/_uwas/dashboard/`) is served with no frame-protection header at all. Exploitability is limited because dashboard auth is a `sessionStorage` Bearer token (no ambient cookie authority), so this is a defense-in-depth gap rather than a directly exploitable clickjacking flaw.

## Defenses observed (good)

- `internal/middleware/headers.go:26` — global `SecurityHeaders()` middleware sets `X-Frame-Options: SAMEORIGIN` on every response from the main public server. It is wired at the very top of the middleware chain (`internal/server/server.go:694`), so all static/PHP/proxy HTML responses are frame-protected against cross-origin framing. `SAMEORIGIN` is sufficient legacy clickjacking protection in all current browsers.
- `internal/admin/api.go:1182` — `jsonResponse()` sets `X-Frame-Options: DENY` (plus `nosniff`, HSTS) on JSON API responses.
- Per-domain `SecurityHeadersConfig` (`internal/config/domain.go:122`) allows operators to add `Content-Security-Policy` (including `frame-ancestors`) per site.
- Dashboard auth uses a `sessionStorage` token sent via `Authorization: Bearer` (`web/dashboard/src/lib/api.ts:5,31`). `sessionStorage` is scoped per top-level browsing context, so a cross-origin attacker iframe gets an empty store and the framed dashboard is unauthenticated — defeating the ambient-authority requirement that classic clickjacking depends on.

## Findings

### CLICK-001: Admin dashboard SPA HTML served without frame-protection headers

- **Severity:** Low
- **Confidence:** 70
- **File:** internal/admin/routes.go:410-426 (admin handler chain lacking `SecurityHeaders`: internal/admin/api.go:246)
- **CWE:** CWE-1021 (Improper Restriction of Rendered UI Layers / Clickjacking)
- **Evidence:**
  ```go
  // internal/admin/routes.go:410
  s.mux.Handle("/_uwas/dashboard/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      ...
      // SPA fallback: serve index.html for all other routes
      indexData, err := fs.ReadFile(distFS, "index.html")
      ...
      w.Header().Set("Content-Type", "text/html; charset=utf-8")
      w.Write(indexData)   // no X-Frame-Options, no CSP frame-ancestors
  }))
  ```
  The admin `http.Server` handler chain is:
  ```go
  // internal/admin/api.go:246
  Handler: middleware.RequestID()(s.authMiddleware(requireJSONMiddleware(s.mux))),
  ```
  It deliberately omits `middleware.SecurityHeaders()` (which is only applied to the separate public-site server). Static dashboard assets served by `http.FileServer` (line 414) likewise carry no frame header, and the dashboard path is treated as a public route by `authMiddleware` (`internal/admin/api.go:306`).
- **Why it's (weakly) exploitable:** Any attacker page can `<iframe src="https://victim:adminport/_uwas/dashboard/">` and overlay it for UI-redress attacks. However, because the dashboard authenticates with a `sessionStorage` Bearer token rather than a cookie, the framed instance loads unauthenticated and the user cannot perform sensitive actions inside the frame. The realistic residual risk is login-form redressing / phishing overlays and missing defense-in-depth on a sensitive admin surface (settings, terminal, file manager, SFTP users). This is why severity is Low rather than High.
- **Remediation:** Add `middleware.SecurityHeaders()` to the admin server's handler chain (`internal/admin/api.go:246`), or explicitly set `w.Header().Set("X-Frame-Options", "DENY")` and `w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")` in `registerDashboardUI` for both the SPA fallback and the `http.FileServer` branch. `DENY` is appropriate here since the dashboard never needs to be framed.
