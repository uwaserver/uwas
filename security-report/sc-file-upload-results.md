# sc-file-upload results

Summary: No critical/high file-upload vulnerabilities. All upload endpoints are authenticated and scoped (RBAC per-domain, or admin+PIN). The file-manager upload accepts any file type into the executable web root, but this is by-design hosting behaviour (cPanel-equivalent) gated by per-domain RBAC and a per-domain PHP sandbox. Two low/info residual-risk notes follow.

## Upload surfaces reviewed

| Endpoint | Handler | Auth | Notes |
|---|---|---|---|
| `POST /api/v1/files/{domain}/upload` | `internal/admin/handlers_files.go:396` | `requireDomainAccess` (RBAC per-domain) | multipart; no type check; writes to web root |
| `POST /api/v1/migrate/cpanel` | `internal/admin/handlers_migrate.go:233` | `requireAdmin` + `requirePin` | tar.gz extraction |
| `POST /api/v1/certs/{host}/upload` | `internal/admin/handlers_migrate.go:286` | `requireAdmin` | JSON PEM; host validated; atomic write |

## Defenses observed (verified)

- Path traversal: `internal/admin/handlers_files.go:429` strips dir components with `filepath.Base`, and `filemanager.safePath` (`internal/filemanager/filemanager.go:148-166`) cleans, rejects abs/`..`, and resolves symlinks (incl. non-existent tails) to keep writes inside the domain root.
- Size limits: file-manager request capped at 100MB (`http.MaxBytesReader`, line 402) and per-file 50MB (line 419).
- cPanel tar extraction (`internal/migrate/cpanel.go:78-104`): rejects entries containing `..` after `filepath.Clean`, joins under a temp dir, and only handles `TypeDir`/`TypeReg` — symlink/hardlink entries are ignored, so no symlink-escape. Domain names from attacker-controlled `userdata` are validated (no `/`, `\`, `..`) at lines 139-143 before being joined into target paths.
- Cert upload validates `host` (`handlers_migrate.go:306`) before joining into `/var/lib/uwas/certs`, writes 0600 atomically.
- RBAC: `canAccessDomain` (`handlers_auth.go:104`) restricts non-admin users to their assigned domains; `app:` file targets require admin (`handlers_files.go:68-74`).

## Findings

### UPLOAD-001: File-manager upload performs no server-side file-type validation and writes into the executable web root
- Severity: Low (by-design hosting feature; mitigated)
- Confidence: 35
- File: `internal/admin/handlers_files.go:416-434`, sink `internal/filemanager/filemanager.go:118-130` (`SaveUpload` → `os.Create`)
- CWE-434 (Unrestricted Upload of File with Dangerous Type)
- Evidence: the loop iterates `r.MultipartForm.File` and calls `filemanager.SaveUpload(root, relPath, src)` with no extension/MIME/magic-byte check. `root` is the domain's served web root, and for `type: php` domains FastCGI executes any `.php` written there.
- Why limited / not escalatable: the endpoint is reached only after `authorizedDomainRoot` → `requireDomainAccess`, so a caller can only write to a domain they already control. A domain owner uploading PHP to their own root is the intended product behaviour (equivalent to SFTP/cPanel file manager), and execution is confined by the per-domain PHP sandbox (`disable_functions`, `open_basedir`, `allow_url_include=Off`). There is no privilege boundary crossed, so this is not a classic upload-to-RCE.
- Residual risk: if an operator grants a low-trust user the `file.upload` capability on a domain intending only "data" uploads, that user effectively gains code execution within that domain's PHP sandbox. Consider an optional per-domain allow/deny extension policy for the file manager for least-privilege deployments.
- Remediation (optional/hardening): offer a configurable server-side extension/MIME allowlist for non-PHP domains; ensure non-PHP (`static`/`proxy`) domain roots never execute uploaded scripts (already the case — static handler does not invoke FastCGI).

### UPLOAD-002: cPanel archive extraction has no aggregate size/entry-count cap (decompression / disk-exhaustion DoS)
- Severity: Info
- Confidence: 30
- File: `internal/migrate/cpanel.go:69-104`
- CWE-409 (Improper Handling of Highly Compressed Data) / CWE-400 (Uncontrolled Resource Consumption)
- Evidence: each regular file is capped at 10GB via `io.LimitReader(tr, 10<<30)`, but there is no cap on the number of entries or total extracted bytes; extraction goes to a temp dir on the system disk.
- Why limited: the endpoint requires `requireAdmin` + `requirePin`, so only a fully-authenticated admin (already trusted) can trigger it, and the request body is capped at 10GB. Disk exhaustion by an admin importing their own backup is low-impact.
- Remediation: track cumulative extracted size and abort past a sane ceiling; bound entry count.
