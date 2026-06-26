# Dependency Audit — UWAS

Scope: Go modules (`go.mod`/`go.sum`) and dashboard npm deps (`web/dashboard/package.json` + `package-lock.json`).
Date: 2026-06-26.

## Tooling

- `govulncheck@v1.1.4` (Go 1.26.4) — reachability-aware scan against `vuln.go.dev` (DB updated 2026-06-25). Result: **No vulnerabilities found.**
- `npm audit` on `web/dashboard` (286 deps: 63 prod, 223 dev) — Result: **0 vulnerabilities** (critical/high/moderate/low all 0).

## Inventory

### Go (ecosystem: Go)
Direct (5), all exact-pinned in `go.mod`, integrity hashes present in `go.sum`:
- `github.com/andybalholm/brotli v1.2.0`
- `github.com/quic-go/quic-go v0.59.0`
- `golang.org/x/crypto v0.52.0`
- `golang.org/x/sync v0.20.0`
- `gopkg.in/yaml.v3 v3.0.1`

Indirect (5): `github.com/kr/text v0.2.0`, `github.com/quic-go/qpack v0.6.0`, `golang.org/x/net v0.55.0`, `golang.org/x/sys v0.45.0`, `golang.org/x/text v0.37.0`.

- No `replace` directives (no local-path / non-standard-URL substitution).
- No `//go:generate` directives, no cgo (`import "C"`) — no build-time code execution or external C toolchain risk.

### npm (ecosystem: npm) — dashboard only, embedded as static assets via go:embed
Direct prod (6): `@xyflow/react ^12.10.1`, `lucide-react ^1.7.0`, `react ^19.2.4`, `react-dom ^19.2.4`, `react-router-dom ^7.13.1`, `recharts ^3.8.0`.
Direct dev (14): vite ^8.0.16, typescript ~5.9.3, eslint ^9.39.4, @playwright/test ^1.58.2, tailwindcss/@tailwindcss/vite ^4.2.2, typescript-eslint ^8.57.2, @types/*, etc.

- `package-lock.json` present (lockfile committed) — transitive versions resolved/pinned.
- All `resolved` URLs point to `registry.npmjs.org` — no mixed/private registry, no dependency-confusion vector. No `.npmrc`.
- No `preinstall`/`install`/`postinstall` lifecycle scripts in the lockfile dependency tree — no install-time script execution risk.
- All direct deps are actively maintained mainstream packages (no abandoned/typosquatted names detected).

## Findings

No known-vulnerable, abandoned, or typosquatted dependencies found. Only low/informational hygiene notes below.

### Finding: DEP-001 — Floating caret ranges in dashboard manifest
- **Severity:** Low
- **Confidence:** 70
- **Package:** all `web/dashboard/package.json` deps (e.g. `vite ^8.0.16`, `react ^19.2.4`)
- **Ecosystem:** npm
- **Vulnerability Type:** Pinned-vs-floating (informational)
- **CWE:** CWE-1357 (Reliance on insufficiently trustworthy component)
- **Description:** `package.json` uses `^`/`~` ranges. This is standard, and `package-lock.json` is committed so CI builds are reproducible. The note is only relevant if a build path ever runs `npm install`/`npm update` without honoring the lockfile (use `npm ci`).
- **Impact:** A fresh `npm install` (vs `npm ci`) could pull newer minor/patch transitives than what was reviewed, widening the supply-chain trust surface.
- **Remediation:** Build the dashboard with `npm ci` so the committed lockfile is authoritative. (Go side already exact-pinned via go.mod + go.sum — no action.)
- **References:** npm-ci docs.

### Finding: DEP-002 — Indirect dep `golang.org/x/net v0.55.0` is reachable surface
- **Severity:** Low
- **Confidence:** 50
- **Package:** golang.org/x/net@v0.55.0
- **Ecosystem:** Go
- **Vulnerability Type:** Outdated/monitor (informational)
- **Description:** `x/net` (HTTP/2, proxy, websocket helpers pulled via quic-go) has historically been a frequent source of HTTP/2-related CVEs. The current pinned version has no advisory per govulncheck (reachability-checked, DB 2026-06-25).
- **Impact:** None currently. Listed so the team keeps `x/net`/`x/crypto`/quic-go bumped promptly when new advisories land, given this is an internet-facing server.
- **Remediation:** Keep `go get -u golang.org/x/net golang.org/x/crypto github.com/quic-go/quic-go` in the routine maintenance cycle; re-run `govulncheck ./...` in CI.
- **References:** https://pkg.go.dev/vuln/

## Dependency Audit Summary
- Total dependencies: ~296 (Go: 10 [direct 5, indirect 5]; npm: 286 [prod 63, dev 223])
- Ecosystems scanned: Go, npm
- Known vulnerabilities found: 0 (Critical: 0, High: 0, Medium: 0, Low: 0)
- Typosquatting risks: 0
- Dependency confusion risks: 0 (all npm from registry.npmjs.org; Go exact-pinned, no local replaces)
- License concerns: 0 (Go deps BSD/Apache/MIT; React/Vite/etc. MIT)
- Outdated dependencies: 0 with CVEs; 2 informational hygiene notes (DEP-001 floating ranges, DEP-002 x/net monitor)
- Build-script risk: none (no npm install scripts, no go:generate, no cgo)
