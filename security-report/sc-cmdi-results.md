# sc-cmdi — OS Command Injection scan results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

**Summary:** No credible OS command-injection vulnerabilities found. UWAS executes external
binaries in ~57 sites; every reachable shell path is either array-form (no shell), guarded by
input validation/allowlisting, or is an intentional admin-only "run this command" feature gated
by `requireAdmin` (RoleAdmin only).

## No issues found by sc-cmdi.

## Scope reviewed

All `exec.Command` / `exec.CommandContext` call sites across `internal/` and `cmd/` were
enumerated and traced from their HTTP/CLI entry points. Particular attention was paid to every
`sh -c` / `bash -c` / `cmd /C` invocation (the only places a shell interprets metacharacters):

| Location | Verdict |
|----------|---------|
| `internal/admin/handlers_software_backup.go:82,200` (tar inside alpine via `sh -c`) | Safe. Backup/restore filenames pass through `sanitizeSoftwareName` (alnum/`-`/`_` only) and `resolveSoftwareBackupPath` (filepath.Base, `.tar.gz` suffix, containment check, must match a known volume key). Admin-only. |
| `internal/database/docker.go:236,317,333` (`docker exec … sh -c`) | Safe. SQL passed via **stdin** (`cmd.Stdin`), not the shell string; the `sh -c` payload is a constant. DB names go through `containerName_safe` / `ValidDBIdentifier` + `backtick`/`escapeSQL`. |
| `internal/admin/handlers_apps_git.go:148`, `internal/deploy/deploy.go:513`, `internal/cronjob/monitor.go:140`, `internal/apps/manager.go:926` (`sh -c command`) | By-design command runners (build steps, cron jobs, app start commands). All admin-only (`requireAdmin` → `RoleAdmin`), and additionally filtered by `validateShellCommand` / `validateBuildCommand` (reject `$(`, backtick, `|`, `<`, `>`, `;`, `&&`, `||`, NUL/CR/LF). An admin running shell commands is the product's purpose (it also ships a terminal/PTY bridge). |
| `internal/admin/handlers_setup.go:76-81`, `internal/cloudflare/cloudflared.go:55-56`, `internal/admin/api.go:768` (`bash -c …`) | Safe. Constant command strings, no user input interpolated. |
| `internal/migrate/sitemigrate.go` & `clone.go` (ssh/rsync/mysqldump) | Safe. `validateSSHInput` parses `source_port` as int, matches `source_host` against `migrateHostRe`, rejects `ssh_key` basenames beginning with `-`, and rejects `source_path` beginning with `-` — explicitly closing the `-oProxyCommand=` argument-injection vector. DB identifiers validated via `validMigrateDBIdentifier`; values shell-quoted in the remote command. |
| Git clone path (`validateGitURL`) | Safe. Rejects `ext::`, `file://`, `--upload-pack`/`--receive-pack`, whitespace/NUL, and non-`https/ssh/git@` schemes — closing the `git clone <url>` argument-injection class. `validGitRef` keeps shell-meaningful chars out of refs. |
| `internal/siteuser/manager.go` (useradd/chpasswd/chown) | Safe. Array form; usernames derived from a regex-validated hostname and `uwas-` prefixed (cannot start with `-`). |
| `internal/middleware/imageopt.go:179,185` (cwebp/avifenc on served files) | Safe. Array form; `src`/`dst` are absolute on-disk paths (leading `/`), so no leading-dash argument injection. |
| `internal/apps/docker.go` (`docker run …`) | Safe. Array form; image/env/port args operator-configured and admin-gated; no shell. |
| `internal/wordpress/installer.go` (mysql `-e`, wp-cli, tar) | Safe. `ValidDBIdentifier` + `BacktickID`/`escSQL`; array form. |
| `firewall/`, `services/`, `doctor/`, `database/manager.go`, `cronjob/manager.go` | Safe. Array form with fixed binaries; numeric/enum/identifier args. |

## Defenses observed

- Array-form `exec.Command(bin, args...)` is the default throughout — no shell interpretation.
- Dedicated allowlist validators: `validateShellCommand`, `validateBuildCommand`,
  `validateGitURL`, `validGitRef`, `validateSSHInput`, `ValidDBIdentifier`,
  `validMigrateDBIdentifier`, `sanitizeSoftwareName`, `containerName_safe`.
- SQL passed via stdin where possible (`database/docker.go`) instead of shell interpolation.
- Every command-execution HTTP handler is behind `requireAdmin` (strict `auth.RoleAdmin`).

## Defense-in-depth observation (not a finding)

`internal/deploy/deploy.go:529 validateShellCommand` forbids `&&`/`||` but not a *single* `&`
(a shell command separator), whereas `internal/admin/handlers_apps_git.go:114 validateBuildCommand`
explicitly rejects a lone `&`. This is not exploitable as a trust-boundary crossing — the command
is an admin-supplied build/deploy command that already runs with full privilege by design — but
aligning the two validators (reject lone `&`) would be a cheap consistency improvement.
