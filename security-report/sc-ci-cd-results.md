# sc-ci-cd — CI/CD Pipeline Security Results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Summary: UWAS CI/CD is generally well-hardened (no `pull_request_target`, no user-controlled `github.event.*` interpolation into shells, secrets passed via `env:` not inline). Findings are limited to a controllable-context shell injection in the release workflow, a missing `permissions` block in `ci.yml`, and unpinned action/tool references.

Scope scanned: `.github/workflows/ci.yml`, `.github/workflows/docs.yml`, `.github/workflows/release.yml`. No GitLab/Jenkins/CircleCI/Travis/Azure pipelines present (the `.travis.yml` hits are inside `node_modules/` vendored deps and are out of scope).

---

## Finding CICD-001: Template injection of `github.ref_name` into `run:` shell (Release workflow)

- Severity: Medium
- Confidence: 55
- File: `.github/workflows/release.yml:104` and `.github/workflows/release.yml:129`
- CWE: CWE-94 (Improper Control of Generation of Code / Expression Injection)

Evidence:
```yaml
# line 104 (Build job)
-ldflags="-s -w -X '...build.Version=${{ github.ref_name }}' -X '...build.Commit=${{ github.sha }}'" \
# line 129 (publish job)
TAG="${{ github.ref_name }}"
```

Why it is (conditionally) exploitable: `${{ github.ref_name }}` is expanded by the Actions templating engine directly into the shell command string before the shell runs. For tag-triggered runs (`on: push: tags: ["v*"]`) `ref_name` is the tag name. Git `check-ref-format` permits `$`, `(`, `)`, `` ` ``, `;`, `&`, `|`, `{`, `}` in ref names (spaces are disallowed but `${IFS}` substitutes for whitespace), so a crafted tag such as `v1$(curl${IFS}attacker)` would execute inside the runner. The job runs with `permissions: contents: write` and a populated `GITHUB_TOKEN`, so successful injection allows token exfiltration / release tampering. Severity is held at Medium because pushing a tag requires repository write access (it cannot be triggered by an outside fork PR) — this is a privilege-escalation / compromised-maintainer defense-in-depth issue rather than an anonymous RCE. `github.sha` (line 104) is a safe hex value.

Remediation: Pass the value through an environment variable so the shell — not the templating engine — receives it:
```yaml
- name: Build
  env:
    REF_NAME: ${{ github.ref_name }}
  run: |
    go build -ldflags="-s -w -X '...build.Version=${REF_NAME}' ..." ...
```
Apply the same pattern to the `publish` job (`TAG="$REF_NAME"`). Optionally validate the tag against `^v[0-9]+\.[0-9]+\.[0-9]+`.

---

## Finding CICD-002: `ci.yml` has no top-level `permissions:` block (excessive default GITHUB_TOKEN scope)

- Severity: Medium
- Confidence: 70
- File: `.github/workflows/ci.yml:1` (no `permissions:` key anywhere in the file)
- CWE: CWE-732 (Incorrect Permission Assignment for Critical Resource)

Evidence: `release.yml` (line 10) and `docs.yml` (line 12) declare explicit `permissions:`, but `ci.yml` declares none. The CI workflow runs untrusted PR code paths (`npm ci`, `npm run build`, `go test`, `playwright test`) on `pull_request` events.

Why it matters: With no explicit block, the job's `GITHUB_TOKEN` inherits the repository/organization default, which on older or permissively-configured repos is `read-write` to all scopes. Because `ci.yml` executes build/test scripts from PR branches (fork code in `npm ci`/`npm run build`/lifecycle scripts), any token broader than read is unnecessary attack surface. The CI jobs only need read access. (Note: GitHub already restricts the token to read-only for fork-originated PRs and for repos defaulting to restricted permissions, which is why this is Medium, not High.)

Remediation: Add a minimal top-level block:
```yaml
permissions:
  contents: read
```
and grant any narrower write scope per-job only where required.

---

## Finding CICD-003: Third-party/first-party actions pinned to mutable tags, not commit SHAs

- Severity: Low
- Confidence: 40
- File: `.github/workflows/ci.yml:16,18,47,96,163,191`; `.github/workflows/release.yml:17,19,40,45,57,81,83,93,109,119`; `.github/workflows/docs.yml:25,27,43,57`
- CWE: CWE-829 (Inclusion of Functionality from Untrusted Control Sphere)

Evidence: All `uses:` references point to movable major-version tags, e.g. `actions/checkout@v6`, `actions/setup-go@v6`, `actions/setup-node@v6`, `actions/upload-artifact@v7`, `actions/download-artifact@v8`, `actions/deploy-pages@v5`.

Why it is low/informational: Every referenced action is first-party (`actions/*`, GitHub-maintained), which the skill explicitly treats as a likely false positive. A moved tag is still a theoretical supply-chain vector if the upstream org were compromised, but the practical risk is low. Reported for completeness only.

Remediation: For defense-in-depth, pin actions to full commit SHAs with a comment naming the version, and/or enable Dependabot for `github-actions` to keep pins fresh.

---

## Finding CICD-004: Build tools installed from mutable `@latest` in CI

- Severity: Low
- Confidence: 45
- File: `.github/workflows/ci.yml:24,27`; `.github/workflows/release.yml:31`
- CWE: CWE-494 (Download of Code Without Integrity Check)

Evidence:
```yaml
go install golang.org/x/vuln/cmd/govulncheck@latest   # ci.yml:24
go install honnef.co/go/tools/cmd/staticcheck@latest  # ci.yml:27
go run golang.org/x/vuln/cmd/govulncheck@latest ...   # release.yml:31
```

Why it is low: `@latest` resolves to whatever the module proxy serves at run time, so a compromised upstream release would execute in CI. These are trusted Go security tools and run in the build/test context (not the release-publish step that holds `contents: write`), limiting blast radius. Informational hardening.

Remediation: Pin tool versions (e.g. `govulncheck@v1.x.y`, `staticcheck@2024.x.x`) and bump via Dependabot.

---

## Defenses observed (no finding)

- No `pull_request_target` usage anywhere — fork PRs run with read-only token in an isolated context.
- No interpolation of attacker-controlled `github.event.pull_request.title/body`, `issue.*`, `comment.body`, or `head_commit.message` into `run:` blocks.
- `release.yml` passes `GITHUB_TOKEN` via `env: GH_TOKEN`, never echoed; no secret is printed/encoded in any step.
- `docs.yml` and `release.yml` declare least-needed `permissions:` blocks; `docs.yml` uses `concurrency` + scoped `pages: write`/`id-token: write` for OIDC Pages deploy.
- Artifact upload/download stays within the same trusted workflow run (no cross-workflow `download-artifact` from untrusted producers → no artifact-poisoning path).
- `npm audit --audit-level=moderate` and `govulncheck` run in CI, providing dependency-vuln gating.
