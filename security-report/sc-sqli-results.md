# sc-sqli — SQL Injection Scan Results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

**Summary:** No credible SQL injection vulnerabilities found. UWAS does not use `database/sql` or any ORM; all SQL is built for the `mysql`/`mariadb` CLI client and every construction path is admin-gated and protected by identifier allowlisting and/or value escaping.

## No issues found by sc-sqli.

## Scope & method

- No `database/sql`, `sql.Open`, `sqlx`, or `gorm` usage anywhere in the codebase (`grep` over `internal/`, `pkg/`, `cmd/` returned nothing). The product manages an external MySQL/MariaDB instance by shelling out to the `mariadb`/`mysql` client (`internal/database/manager.go:runMySQL` → `runMySQLOnHost`, SQL passed via `-e` argument through `exec.Command`, not a shell).
- SQL string construction is limited to four packages: `internal/database`, `internal/migrate`, `internal/wordpress`, `internal/admin`. A grep for SQL keywords inside `Sprintf` outside these packages returned nothing.
- Every SQL-building HTTP handler is behind `requireAdmin` (and destructive ones add `requirePin`), e.g. `internal/admin/handlers_database.go` handlers `handleDBCreate`, `handleDBDrop`, `handleDBChangePassword`, `handleDBRemoteAccess`, `handleDBExplore*`.

## Defenses observed (verified, not findings)

1. **Identifier allowlisting** — `internal/database/manager.go:904 validDBIdentifier` restricts database/table/user identifiers to `[A-Za-z0-9_]`, max 64 chars, rejecting a leading `-`. Applied to DB names, table names, and usernames in `CreateDatabase`, `DropDatabase`, `ConfigureRemoteAccess`, the DB explorer (`handleDBExploreTables/Columns/Query` via `database.ValidDBIdentifier`), and `internal/wordpress/installer.go:createMySQLDB`.
2. **Identifier quoting** — `backtick()` (`manager.go:861`) wraps identifiers in backticks and doubles embedded backticks, used everywhere an identifier is interpolated (`USE %s`, `CREATE DATABASE %s`, `GRANT ... ON %s.*`).
3. **Value escaping** — `escapeSQL()` (`manager.go:922`), `escSQL()` (`wordpress/installer.go:42`), and `sqlString()` (`migrate/sql_safety.go:17`) backslash-escape `\` then `'`/`"` and strip NULs before interpolation into single-quoted literals (user/host/password). The escape order (backslash first) is correct.
4. **DB Explorer (`handleDBExploreQuery`, handlers_database.go:625)** is an intentional admin SQL console, but is constrained: read-only allowlist (`SELECT`/`SHOW`/`DESCRIBE`/`EXPLAIN` only, after stripping leading `/* */`, `--`, `#` comments), stacked-statement block (`strings.Contains(req.SQL, ";")`), and blocks `INTO OUTFILE`/`DUMPFILE`/`LOAD_FILE`/`FOR UPDATE`/`LOCK IN SHARE MODE`. The `db` path value is identifier-validated before `USE %s`.

## Minor hardening notes (not exploitable, informational only)

- `ChangePassword` (`manager.go:528`) interpolates `user`/`host` with `escapeSQL` but does not also call `validDBIdentifier` on `user` (other paths do). Escaping makes quote-breakout infeasible, and the endpoint is admin-only, so this is not exploitable — but adding the identifier check would match the surrounding code and provide defense-in-depth.
- All quote escaping relies on MySQL backslash escaping. It is effective under default SQL mode but would be weakened if the server were configured with `NO_BACKSLASH_ESCAPES`/ANSI mode. Since these paths are admin-only and the server config is operator-controlled, risk is negligible. Switching to client-side prepared statements (e.g. a real driver) would remove the assumption entirely.
