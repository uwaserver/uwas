# sc-lang-go — Go Security Deep Scan Results

**Summary:** UWAS is an unusually well-hardened Go codebase; the standard Go anti-patterns (path traversal, command injection, zip-slip, missing server timeouts, weak randomness for secrets, non-constant-time auth compares, unbounded body reads) are all mitigated. Only two low-severity Go-idiomatic issues survived verification, both gated behind admin authentication.

---

## Defenses observed (verified, NOT findings)

- **Path traversal:** `internal/pathsafe`, `internal/filemanager/filemanager.go:safePath`, `internal/sftpserver/server.go:safePath`, and `internal/handler/fastcgi/handler.go:tryServeFile` all use symlink-resolving containment (`IsWithinBase` + `IsWithinBaseResolved`), reject `..` pre-clean, and SFTP opens with `O_NOFOLLOW` to close the TOCTOU window.
- **Command injection:** `os/exec` shell use (`internal/admin/handlers_apps_git.go`, `internal/deploy/deploy.go`) is admin-only, passes args via `exec.Command(name, args...)` where possible, and shell pipelines pass through `validateShellCommand`/`validateBuildCommand`/`validateGitURL` (reject `$( ) ; \` | < >` and null/control bytes). The `apt list` call in `api.go:768` is a constant string.
- **Zip-slip:** tar extraction in `internal/migrate/cpanel.go` and `internal/backup/backup.go` rejects `..`, uses `safeRestorePath`/`IsInsideDir`, skips symlink/hardlink/device entries, strips SUID/SGID, and bounds per-file and total size.
- **HTTP server timeouts:** public servers set ReadHeader/Read/Write/Idle timeouts (`internal/server/server.go:888,920`).
- **Body size limits:** `http.MaxBytesReader` applied across ~80 admin endpoints; WAF body scan uses `io.LimitReader` + `MultiReader` restore (`internal/middleware/security.go:132`).
- **Crypto:** secrets use `crypto/rand`; `math/rand/v2` only used for load-balancer/canary/mirror traffic distribution (non-security). Auth compares use `crypto/subtle.ConstantTimeCompare` throughout (`internal/auth/manager.go`, `internal/admin/totp.go`, `basicauth.go`).
- **unsafe.Pointer:** only in `internal/terminal/pty_linux.go` (ioctl structs) and `internal/cli/pidcheck_windows.go` (Win32 syscall) — correct, idiomatic syscall usage, no attacker-controlled pointer arithmetic.

---

## Findings

### [LOW] http.DefaultClient used without timeout or request context in Cloudflare admin handlers

- **Category:** #10 net/http Missing Timeouts
- **Location:** `internal/admin/handlers_cloudflare.go:201,236,342`; `internal/admin/handlers_cloudflare_zones.go:43,50` (and `:123,125`)
- **Pattern Matched:** `http.NewRequest(...)` + `http.DefaultClient.Do(req)` — no `Timeout`, no `NewRequestWithContext`.
- **Description:** These admin handlers build outbound requests with `http.NewRequest` (no context) and execute them with `http.DefaultClient`, which has a zero timeout (wait forever). If the upstream (`api.cloudflare.com`) — or a network MITM / DNS hijack holding the TCP socket open — never responds, the handler goroutine blocks indefinitely. The request context is not propagated, so client disconnect does not cancel the call.
- **Exploitability:** Requires authenticated admin to trigger the endpoint, and depends on a stalled/hostile upstream. Impact is a hung goroutine / slow resource accumulation rather than RCE or data exposure. Contrast with `internal/tls/manager.go:267,797` which correctly use `http.NewRequestWithContext` + `context.WithTimeout`.
- **Remediation:** Use a package-level `*http.Client{Timeout: ...}` (as already done in `internal/cloudflare/client.go:30`) and `http.NewRequestWithContext(r.Context(), ...)` so calls are bounded and cancelable.
- **Reference:** CWE-1088 / CWE-400.

### [INFO] Per-domain reverse-proxy TLS verification can be disabled

- **Category:** #7 TLS Configuration Mistakes
- **Location:** `internal/handler/proxy/handler.go:82` (`InsecureSkipVerify: true`); config at `internal/config/proxy.go:22`
- **Pattern Matched:** `tls.Config{InsecureSkipVerify: true}`
- **Description:** When an operator sets `proxy.insecure_skip_verify: true` for a domain, the upstream HTTPS connection skips certificate verification, exposing that origin leg to MITM. This is an explicit, documented opt-in (annotated `#nosec G402`) intended for self-signed / private-CA origins.
- **Exploitability:** Not attacker-reachable — it is a deliberate operator configuration choice, not a default. Listed only because the skill flags the pattern. No code change recommended beyond ensuring docs warn against using it with untrusted networks; consider supporting a custom CA bundle (`RootCAs`) as the safer alternative.
- **Reference:** CWE-295.

---

## Notes / out of scope for this skill

- The WAF skips body inspection for `Content-Type: application/json` and several other types (`internal/middleware/security.go:isAPContentType`). This is a defense-in-depth tuning tradeoff (avoids false positives on JSON), not a Go anti-pattern; downstream handlers still enforce their own validation. Flagged to the OWASP/WAF reviewer, not counted here.
