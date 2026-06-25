# Contributing to UWAS

Thank you for your interest in contributing to UWAS.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/uwas.git`
3. Create a topic branch (see [Branch Naming](#branch-naming) below)
4. Make your changes — one logical concern per commit
5. Build the dashboard: `make dashboard` (embeds into the Go binary via `go:embed`)
6. Run the pre-push gate: `make check` (lint + TypeScript typecheck + tests)
7. Commit with a [conventional commit message](#commit-format)
8. Push and open a Pull Request

> **Note:** Dashboard changes require `npm install` in `web/dashboard/` first.
> `make check` runs lint + `tsc -b` + `go test` in one shot.

### Branch Naming

Use a descriptive prefix + kebab-case topic:

| Prefix | When | Example |
|--------|------|---------|
| `feat/` | New feature | `feat/multi-domain-cache` |
| `fix/` | Bug fix | `fix/sftp-chroot-escape` |
| `refactor/` | Code reorganization (no behavior change) | `refactor/split-cloudflare-handlers` |
| `docs/` | Documentation only | `docs/docker-quickstart` |
| `chore/` | Tooling, deps, CI | `chore/bump-go-version` |

Always branch from `main`. Delete the branch after merge.

### Commit Format

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
type: short imperative description

Optional body explaining WHY (not what — the diff shows what).
```

**Types:** `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`

Rules:
- Subject ≤ 72 chars, imperative mood, no trailing period
- One concern per commit — never mix logic changes with lockfile updates
- Reference issues: `Fix #123` or `Closes GH-456`

```
# Good
fix: correct race condition in token refresh

Retry logic now respects backoff multiplier. Without this,
repeated failures would hammer the provider instead of backing off.

# Bad
fix: fixed bug
```

### Pull Requests

- One feature or fix per PR
- Include tests for new functionality
- Update documentation if behavior changes
- Keep PRs small and focused — split large work into stacked PRs

## Development

### Requirements

- Go 1.26 or later
- Node.js 22+ (dashboard development only)
- Docker (optional, for containerized testing)

### Build

```bash
make dev     # Development binary
make build   # Production binary (stripped, versioned)
```

### Test

```bash
make test    # go test ./... (parallel, ~4 min)
make lint    # go vet + staticcheck
```

CI runs a separate race detector job (`go test -race`). To run it locally
before pushing concurrent code:

```bash
go test -race ./internal/cache/... ./internal/router/...   # specific packages
go test -race ./...                                        # all (skip backup: needs MySQL socket)
```

### Docker Development

The project ships a Dockerfile and docker-compose setup for containerized
development and testing. This is the fastest way to verify that changes work
in the production runtime environment (non-root user, volume-seeded config).

#### Build the image

```bash
make build        # production binary (needed before Docker build embeds it)
docker build -t uwas:dev .
```

> **Note:** The Dockerfile builds the Go binary itself, so `make build` is only
> needed if you want to skip the in-image build via multi-stage caching.

#### Quick smoke test

```bash
docker run -d -p 9443:9443 -e UWAS_ADMIN_KEY=dev-key-123 \
  -v uwas_dev_config:/etc/uwas uwas:dev
```

The dashboard is at `http://localhost:9443/_uwas/dashboard/`. The healthcheck
hits `/api/v1/health` automatically.

#### Full stack with compose

```bash
cp .env.example .env       # set UWAS_ADMIN_KEY + DB passwords
docker compose up -d       # starts UWAS + PHP-FPM + MariaDB
```

This mounts a named volume at `/etc/uwas` so domain additions persist. See
[`docker/README.md`](docker/README.md) for volume management, reseeding, and
troubleshooting.

#### Iterating on changes

The Dockerfile copies the full source, so any Go or dashboard change requires
a rebuild. For fast iteration on the dashboard alone, run the Vite dev server
on the host and point it at a running container:

```bash
cd web/dashboard && npm run dev    # Vite dev server (hot reload)
```

For Go changes, rebuild the image:

```bash
docker build -t uwas:dev . && docker compose up -d --force-recreate uwas
```

#### Verifying non-root hardening

The image runs as the `uwas` user with `CAP_NET_BIND_SERVICE`. Confirm it from
inside the container:

```bash
docker exec <container> id          # should show uid!=0
docker exec <container> uwas version
```

The dashboard's UWAS card shows the runtime environment (`docker · non-root`)
when running in a container — see `/api/v1/system`.

### Project Structure

```
cmd/uwas/            CLI entry point
internal/
  server/            HTTP/HTTPS/HTTP3 server + request dispatch
  admin/             REST API (254+ routes) + dashboard embed
    api.go            Core: Server struct, lifecycle, middleware, helpers
    routes.go         Route registration (themed sub-registrars)
    handlers_*.go     Topic-split handlers (one file per feature area)
  config/            YAML structs, validation, defaults
  router/            Virtual host routing (SNI + Host header)
  middleware/        Chain composition, WAF, rate limit, compression, CORS
  handler/
    static/          Static file serving
    fastcgi/         PHP-FPM handler
    proxy/           Reverse proxy + load balancer
  cache/             L1 memory (256-shard LRU) + L2 disk + ESI
  auth/              Multi-user RBAC + sessions + TOTP
  apps/              Standalone app supervision (Node/Python/Ruby/Go/Docker)
  tls/               TLS manager + ACME client
pkg/
  fastcgi/           FastCGI binary protocol (public)
  htaccess/          .htaccess parser (public)
web/dashboard/       React 19 SPA (Vite + TypeScript + Tailwind)
```

Key dependency direction: `server → admin → config`, `server → router → middleware → handler/*`.
Never import `internal/admin` from other packages — it's the top of the dependency chain.

### How to Add Common Features

| Task | Steps |
|------|-------|
| **New admin endpoint** | 1. Add route in `internal/admin/routes.go` (themed sub-registrar). 2. Add handler in the matching `handlers_*.go`. 3. Call `requireAdmin` for privileged ops. 4. Add test in the same `handlers_*.go` test file. |
| **New config field** | 1. Add struct field in `internal/config/config.go`. 2. Add default in `defaults.go`. 3. Add validation in `validate.go`. 4. Wire into Settings API in `handlers_settings.go` (both Get + Put). 5. Update `uwas.example.yaml`. |
| **New middleware** | 1. Create in `internal/middleware/`. 2. Add to chain in `server.go:buildMiddlewareChain()`. 3. Test with `httptest`. |
| **New dashboard page** | 1. Create in `web/dashboard/src/pages/`. 2. Add route in `App.tsx`. 3. Add to `Sidebar.tsx`. 4. Add API function in `lib/api.ts`. |
| **New CLI command** | 1. Create in `internal/cli/`. 2. Register in `cmd/uwas/main.go`. |

## Security Guidelines

UWAS runs as a web server with root-level operations (TLS, firewall, PHP).
Contributors must follow these rules:

- **Path traversal:** All file operations must go through `internal/pathsafe`
  (`IsWithinBaseResolved`). Never trust user input for file paths.
- **Authorization:** Every state-changing endpoint must call `requireAdmin`
  or `requirePin` (for destructive ops). Never skip auth checks.
- **Command injection:** Use `exec.Command` with arg arrays, never
  `exec.Command("sh", "-c", userInput)`. Validate all inputs.
- **Secrets:** Never log secrets (API keys, passwords, tokens). Use
  `maskSecret()` or `maskYAMLValue()` before returning to the client.
- **Input validation:** Validate all request bodies with `json.Decode` +
  field checks. Use `http.MaxBytesReader` on all POST/PUT endpoints.
- **SSRF:** External URL fetching (webhooks, notifications) must use
  `config.SafeDialControl` to block private IP ranges.

## Review Process

1. All PRs require at least one review before merge.
2. CI must pass: `go vet` + `staticcheck` + `govulncheck` + `go test` + race
   detector + dashboard `tsc -b` + docs build.
3. Security-sensitive changes (auth, pathsafe, middleware, Dockerfile) require
   extra attention — flag them in the PR description.
4. Squash-merge to `main`. The commit message should follow the Conventional
   Commits format from the PR title.

## Guidelines

### Code

- Follow standard Go conventions (`gofmt`, `go vet`, `staticcheck`)
- Use `log/slog` via `internal/logger` for all logging
- Prefer stdlib over external dependencies (current count: 5 direct deps)
- Write tests for new functionality — target ≥70% coverage for new code
- Keep files focused: one topic per file (see `handlers_*.go` pattern)

### Issues

- Open an issue before starting significant work
- Use issue templates when available
- Include reproduction steps for bug reports

## Code of Conduct

Be respectful and constructive. We're here to build great software together.

## License

By contributing, you agree that your contributions will be licensed under the [AGPL-3.0](LICENSE) and may also be included in commercially licensed versions of UWAS.
