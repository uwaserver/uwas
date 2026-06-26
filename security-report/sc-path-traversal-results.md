# sc-path-traversal results

**Summary:** No credible, reachable path-traversal / LFI / RFI / zip-slip / symlink-escape vulnerabilities were found. UWAS applies a centralized, symlink-aware containment guard (`internal/pathsafe`) and input validation consistently across every user-reachable file sink that was traced.

No issues found by sc-path-traversal.

---

## Scope traced

Every place where HTTP/CLI/SFTP/upload/archive input flows into a filesystem path was inspected:

- File manager (`internal/filemanager`, `internal/admin/handlers_files.go`)
- Static file serving + try_files (`internal/handler/static/handler.go`)
- FastCGI X-Accel-Redirect / X-Sendfile (`internal/handler/fastcgi/handler.go`)
- SFTP chroot jail (`internal/sftpserver/server.go`)
- Backup restore + cPanel import (tar extraction) (`internal/backup/backup.go`, `internal/migrate/cpanel.go`)
- WordPress download/extract (`internal/wordpress/installer.go`)
- Domain YAML / cert persistence (`internal/admin/handlers_domain.go`, `handlers_migrate.go`)
- Software store / docker compose files (`internal/admin/handlers_software_store.go`)
- App logs / WP debug log readers (`internal/admin/handlers_apps.go`, `handlers_wordpress.go`)
- L2 disk cache file paths (`internal/cache/disk.go`, `key.go`)
- Per-location static roots and directory listing (`internal/server/server_dispatch.go`)

---

## Defenses observed (why nothing was reportable)

1. **Central containment guard — `internal/pathsafe/pathsafe.go`.**
   `IsWithinBase` (lexical) + `IsWithinBaseResolved` (resolves `filepath.EvalSymlinks`, including non-existent tails via `resolvePath`) reject both `../` traversal and symlinked-parent escape. Used by the static handler, file manager, X-Sendfile/X-Accel handler, SFTP, and per-location roots.

2. **File manager `safePath` (`internal/filemanager/filemanager.go:148`)** rejects absolute paths and leading `..`, anchors with `filepath.Join`, then applies both lexical and symlink-resolved containment. All read/write/delete/mkdir/upload operations route through it. Uploads additionally strip directory components with `filepath.Base` (`handlers_files.go:429`).

3. **Static handler (`internal/handler/static/handler.go`)** rejects dotfile path components, anchors candidate paths with `filepath.Clean("/"+...)`, and verifies `base.Contains(fullPath)` before `os.Stat`/serve.

4. **X-Sendfile / X-Accel-Redirect (`internal/handler/fastcgi/handler.go:194`)** blocks any `..` after `Clean`, then validates the target against `domain.Root` (+ explicitly configured `InternalAliases`) with both lexical and symlink-resolved checks. A compromised PHP app cannot stream `/etc/passwd`.

5. **SFTP (`internal/sftpserver/server.go:314`)** rejects `..` pre-clean, anchors to the chroot root, applies both containment checks, and opens files with `O_NOFOLLOW` (`nofollow_unix.go`) to close the symlink TOCTOU window.

6. **Archive extraction.** Backup restore uses a `safeRestorePath` allowlist by prefix + containment (`internal/backup/backup.go:976`); cPanel import rejects entries containing `..` after `Clean` and extracts to a temp dir, and validates discovered domain names (`internal/migrate/cpanel.go:79,139`). No zip-slip path was reachable.

7. **Domain/cert/software identifiers** are sanitized before use as filenames: `domainFilePath` rejects `/`, `\`, `..` and applies `filepath.Base` (`handlers_domain.go:1405`); cert host upload rejects `/`, `\`, `..` (`handlers_migrate.go:306`); software names pass through `appNameLike` (alphanumerics/`-`/`_` only).

8. **Disk cache** derives the on-disk filename from an FNV-1a hash of the cache key (`internal/cache/disk.go:148`), never the raw URL, so attacker-controlled URLs cannot steer the file path.

9. **RBAC gating.** File-manager and admin file operations require `authorizedDomainRoot` → `requireDomainAccess`/`requireAdmin`; route `{domain}` is a single Go `ServeMux` path segment (cannot contain `/`).

## Minor note (defended, not a finding)

- `domainroot.Fallback(webRoot, domain)` (`internal/domainroot/domainroot.go:124`) builds `webRoot/<domain>/public_html` for domains not present in config. Because `{domain}` is a single non-slash ServeMux segment and the path appends a fixed `public_html` suffix, no multi-level traversal to sensitive files is achievable, and the call is reachable only after RBAC authorization (admins for unknown domains). Not exploitable.
