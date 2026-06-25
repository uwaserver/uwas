# AGENT_DIRECTIVES.md

Mandatory working rules for any AI agent (Claude Code or otherwise) making
changes in this repository. `CLAUDE.md` references this file and summarizes
these rules; this is the authoritative, full version.

---

## 1. Forced verification

Never report a task complete without running the compiler, linter, and tests.

- **Go:** `go build ./...` → `go vet ./...` → `go test ./... -count=1`
  (tests run in parallel by default; Docker tests use unique ports/PIDs).
  Use `go test -race ./...` to check for data races (skip `internal/backup` —
  needs a local MySQL socket not available in CI).
- **Dashboard:** `cd web/dashboard && npx tsc -b` (and `npm run lint` when
  touching `.ts/.tsx`).
- Run `staticcheck ./...` and `gofmt -l` on changed files before committing.

If a check fails, report the failure with its output — do not claim success.

## 2. Phased execution

Multi-file refactors must be broken into phases of at most 5 files each.

- State the plan before starting.
- Verify (build + vet + relevant tests) after every phase.
- Do not begin the next phase until the current one is green.

## 3. Context decay

After 10+ messages in a session, re-read files before editing them. Do not
trust memory of file contents — the file may have changed, or your recollection
may be stale.

## 4. Edit integrity

- Re-read a file immediately before editing it.
- Never batch more than 3 edits to the same file without a verification read
  in between.
- Match existing surrounding style (naming, comment density, idioms).

## 5. Atomic commits

- One logical change per commit.
- Never mix refactor + feature, or cleanup + bugfix, in a single commit.
- Branch off `main` before committing; do not commit or push unless the user
  asks.
- End commit messages with the required co-author trailer.

---

## Conventions (see CLAUDE.md for the full list)

- Go 1.26+, stdlib-first (5 direct deps); no web frameworks, ORMs, or logging
  frameworks.
- Use `internal/logger/` (slog wrapper), not the stdlib `log`.
- Add new config fields in `internal/config/config.go`.
- Add admin endpoints in `internal/admin/routes.go` (themed sub-registrar); put
  handler logic in the matching `handlers_*.go` file (e.g. DNS →
  `handlers_dns.go`, software library CRUD → `handlers_software_library.go`).
  `api.go` is reserved for core (Server struct, lifecycle, middleware, helpers).
- Tests live alongside source (`foo.go` → `foo_test.go`).
- Persist config/domain files atomically (temp + fsync + rename).
