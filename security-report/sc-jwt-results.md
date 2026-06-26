# sc-jwt — JWT Implementation Flaw Scan

**Summary:** No issues found by sc-jwt. UWAS does not use JWTs for its own
authentication; it relies on opaque, server-side, random session tokens and
SHA256-hashed API keys. The only JWS/JWT-shaped code is an outbound ACME client
signer (ES256), which is correct and not attacker-reachable.

No issues found by sc-jwt.

## Scope traced

Searched the whole tree (`*.go`, `*.ts`, `*.tsx`, excluding `_test.go`, e2e,
spec) for: `jwt`, `jsonwebtoken`, `jose`, `HS256`/`RS256`/`ES256`, `alg:none`,
`jwt.Parse`/`Verify`/`decode`, `kid`, `Bearer`, `localStorage`/`sessionStorage`.

## Why there is no JWT attack surface (defenses observed)

1. **No JWT verification anywhere.** `go.mod` has no JWT/JOSE dependency
   (`golang-jwt`, `lestrrat/jwx`, etc.), and there is no `jwt.Parse`,
   `jwt.Verify`, `parseSigned`, or `alg`-from-header handling on any inbound
   request path. Grep for inbound-token parsing returned nothing.

2. **Auth uses opaque server-side tokens, not JWTs.**
   `internal/auth/manager.go:386` issues session tokens via `generateToken()`
   (32 bytes from `crypto/rand`, base64url). `ValidateSession`
   (`manager.go:462`) validates by map lookup + expiry check — there is no
   client-supplied signature to confuse. The `Bearer` path
   (`manager.go:874-880`) routes to `AuthenticateAPIKey`, which compares a
   SHA256 hash via `crypto/subtle.ConstantTimeCompare` (`manager.go:412,434`).
   So `alg:none`, RS256→HS256 confusion, and `kid` injection are all
   structurally impossible here.

3. **`jwtSecret` is vestigial.** `internal/auth/persist.go:29`
   (`loadOrCreateJWTSecret`) and `manager.go:134` persist a 32-byte secret, but
   it is never read to sign or verify anything (confirmed by grep: only the
   load/store sites reference `jwtSecret`). It is dead state, not a weak signing
   key, because nothing signs tokens with it. Not exploitable; worth a cleanup
   note only.

4. **ACME JWS signer is outbound and correct.** `internal/tls/acme/jws.go:31`
   builds a JWS protected header with `alg: ES256` and signs requests *to* the
   ACME server with the server's own ECDSA account key
   (`jws.go:51-59`). This is the server acting as a JWS *producer* for Let's
   Encrypt; it never verifies attacker-controlled tokens, the algorithm is
   fixed (not header-derived), and the `kid`/`jwk` fields are the server's own.
   No confusion or injection surface.

5. **Cloudflare tunnel token** (`internal/cloudflare/client.go:122`) is an
   opaque connector token fetched from the Cloudflare API and passed to
   `cloudflared`; it is never parsed/verified by UWAS. Out of scope.

6. **Dashboard token storage** (`web/dashboard/src/lib/api.ts:5-17`) keeps the
   API key / session token in `sessionStorage` (not `localStorage`, so it is
   cleared on tab close) and sends it as `Authorization: Bearer` or
   `X-Session-Token`. These are opaque tokens, not JWTs, so the JWT-in-storage
   class does not apply. (Generic bearer-token-in-web-storage XSS exposure is a
   separate concern better assessed by the XSS/session scanners, not sc-jwt.)

## False-positive guards applied

- `alg: ES256` in `jws.go` is an outbound signer, not an inbound verifier — excluded.
- `jwtSecret` naming does not imply JWT use; traced and confirmed unused — excluded.
- `sessionStorage` token is not a JWT — excluded from this skill.
