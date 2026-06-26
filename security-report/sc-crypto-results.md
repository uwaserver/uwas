# sc-crypto results

Summary: UWAS has strong cryptographic hygiene — bcrypt for passwords, crypto/rand everywhere, HMAC-SHA256 with constant-time comparison for webhooks/API keys, ECDSA-ES256 for ACME, ed25519 for SSH/host keys, TLS 1.2 floor with modern ciphers. No MD5, no symmetric-cipher misuse (no ECB/CBC/static IV), no hardcoded keys. Two minor (low) observations below; neither is a clear-cut exploitable bug.

---

## Finding CRYPTO-001: Per-domain proxy can disable upstream TLS certificate verification

- **Severity:** Low (opt-in; off by default)
- **Confidence:** 45
- **File:** internal/handler/proxy/handler.go:81-82 (config: internal/config/proxy.go:18-22)
- **CWE:** CWE-295 (Improper Certificate Validation)
- **Evidence:**
  ```go
  if domain.Proxy.InsecureSkipVerify {
      t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — opt-in via per-domain config
  }
  ```
- **Why it matters:** When an operator enables `insecure_skip_verify` for a reverse-proxy domain, the upstream HTTPS connection accepts any certificate, allowing a man-in-the-middle on the UWAS→backend hop. The field is settable through the domain config (YAML) and is reachable from the request path.
- **Mitigation already present:** Defaults to `false`, requires explicit per-domain opt-in, documented and `#nosec`-annotated. This is an intentional feature (e.g. self-signed internal backends), not an accidental disable.
- **Remediation:** Keep default false; consider warning in the dashboard/audit log when enabled, and prefer a per-domain trusted CA bundle (`RootCAs`) over fully skipping verification.

---

## Finding CRYPTO-002: WordPress download integrity verified with SHA1

- **Severity:** Low
- **Confidence:** 30
- **File:** internal/wordpress/installer.go:261-299, internal/wordpress/updater.go:88-90
- **CWE:** CWE-328 (Use of Weak Hash)
- **Evidence:** `hashFileSHA1(tarPath)` is compared against `fetchWPChecksum(url + ".sha1")`. wordpress.org only publishes `.sha1`/`.md5` (not `.sha256`).
- **Why it (barely) matters:** SHA1 is collision-broken. Here it is an integrity check, not a security signature — both the tarball (`https://wordpress.org/latest.tar.gz`) and the `.sha1` checksum are fetched over HTTPS from the same origin, so a practical attack would require breaking TLS to both, at which point the attacker controls the bytes anyway. This falls under the skill's documented "content hashing / integrity" false-positive category and is reported only for completeness.
- **Remediation:** Low priority. If WordPress ever publishes a SHA-256 sidecar, prefer it; otherwise the HTTPS transport is the actual integrity guarantee.

---

## Defenses observed (no findings)

- **Passwords:** `bcrypt.GenerateFromPassword(..., DefaultCost)` for admin users (internal/auth/manager.go), SFTP/site users (internal/sftpserver/server.go:808), legacy plaintext rejected.
- **API keys:** hashed with SHA256 (high-entropy keys, appropriate) and compared via `crypto/subtle.ConstantTimeCompare` (internal/auth/manager.go:434).
- **Secrets/tokens/passwords/PINs:** all generated with `crypto/rand` and panic on failure (siteuser, database, cloudflare, cli/init, software-store, handlers_settings, router/context, middleware/requestid).
- **HMAC:** webhook signatures use HMAC-SHA256 with constant-time compare (internal/admin/handlers_apps_webhook.go:457-470, internal/webhook/manager.go:367, internal/server/server.go:1357).
- **TOTP:** HMAC-SHA1 per RFC 6238 (standard; not a weakness), constant-time code compare (internal/admin/totp.go:50). WebSocket Sec-WebSocket-Accept uses SHA1 per the WS RFC (internal/terminal/handler.go:201) — mandated by spec, not a weakness.
- **TLS server:** `MinVersion: VersionTLS12`, AEAD-only cipher suites, X25519/P256 curves (internal/tls/manager.go:830-848). The `ssl.min_version` config field is validated but the effective floor is hardcoded to 1.2, so a misconfig cannot downgrade below 1.2 (fail-secure).
- **ACME:** ES256 (ECDSA-SHA256) signing with `rand.Reader` (internal/tls/acme/jws.go).
- **Asymmetric keys:** ed25519 for deploy keys and SFTP host keys (internal/admin/handlers_apps_keys.go:78, internal/sftpserver/server.go:775).
- **No symmetric encryption** in the codebase: no `aes.NewCipher`/ECB/CBC/static IV/hardcoded key patterns. No `math/rand` used for security (only proxy load-balancing/canary/mirror selection).
- **JWT secret:** 32-byte crypto/rand, persisted at 0600 in a 0700 dir (internal/auth/persist.go).
