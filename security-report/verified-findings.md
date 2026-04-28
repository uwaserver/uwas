# Verified Findings — After False Positive Elimination

## Total: 16 Verified, 3 False Positives Eliminated

### False Positives Eliminated

| # | Original Finding | Reason for Dismissal |
|---|-----------------|---------------------|
| 1 | Config PUT missing MaxBytesReader | `MaxBytesReader` IS present at `api.go:3134` |
| 2 | X-Forwarded-For spoofing | Rate limiter has proper trusted-proxy model |
| 3 | Rewrite regex ReDoS | Go RE2 engine is O(n), immune to backtracking |

---

### Verified Findings (16)

| # | ID | Severity | Finding | File:Line | CWE |
|---|-----|----------|---------|-----------|-----|
| 1 | C1 | CRITICAL | SFTP plaintext password comparison | `sftpserver/server.go:83` | CWE-256 |
| 2 | C2 | CRITICAL | Deploy hook `sh -c` shell injection | `deploy/deploy.go:410` | CWE-78 |
| 3 | H1 | HIGH | SFTP user delete missing authz | `admin/api.go:3054` | CWE-862 |
| 4 | H2 | HIGH | SSH key handlers missing authz | `admin/handlers_hosting.go:778-850` | CWE-862 |
| 5 | H3 | HIGH | Tar restore untrusted hdr.Mode | `backup/backup.go:445` | CWE-732 |
| 6 | H4 | HIGH | Cert upload rejects all domains | `admin/handlers_hosting.go:2477` | CWE-20 |
| 7 | H5 | HIGH | SQL explorer INTO OUTFILE bypass | `admin/handlers_hosting.go:2410-2433` | CWE-89 |
| 8 | M1 | MEDIUM | Self-update no binary signing | `selfupdate/updater.go:129-186` | CWE-494 |
| 9 | M2 | MEDIUM | WordPress no checksum verification | `wordpress/installer.go:231-278` | CWE-494 |
| 10 | M3 | MEDIUM | API keys plaintext in users.json | `auth/manager.go:40,499` | CWE-256 |
| 11 | M4 | MEDIUM | SSH password in process args | `migrate/sitemigrate.go:165,201` | CWE-214 |
| 12 | M5 | MEDIUM | crypto/rand error unchecked | `admin/api.go:5038-5048` | CWE-330 |
| 13 | M6 | MEDIUM | No per-account lockout | `admin/audit.go:20-32` | CWE-307 |
| 14 | L1 | LOW | GitLab token non-constant-time | `admin/handlers_app.go:290` | CWE-208 |
| 15 | L2 | LOW | TOTP in query param | `admin/api.go:738` | CWE-598 |
| 16 | L3 | LOW | BasicAuth plaintext fallback | `middleware/basicauth.go:115` | CWE-256 |
