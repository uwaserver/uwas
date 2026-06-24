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
make test    # go test ./...
make lint    # go vet + staticcheck
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

- `cmd/uwas/` — CLI entry point
- `internal/` — Private packages (core server logic)
- `pkg/` — Public reusable packages (fastcgi protocol, htaccess parser)
- `docs/` — Design documents
- `test/` — Integration and benchmark tests

## Guidelines

### Code

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `log/slog` via `internal/logger` for all logging
- Prefer stdlib over external dependencies
- Write tests for new functionality

### Commits

- One logical change per commit
- Use clear, descriptive commit messages
- Reference issue numbers when applicable

### Pull Requests

- One feature or fix per PR
- Include tests
- Update documentation if behavior changes
- Keep PRs small and focused

### Issues

- Open an issue before starting significant work
- Use issue templates when available
- Include reproduction steps for bug reports

## Code of Conduct

Be respectful and constructive. We're here to build great software together.

## License

By contributing, you agree that your contributions will be licensed under the [AGPL-3.0](LICENSE) and may also be included in commercially licensed versions of UWAS.
