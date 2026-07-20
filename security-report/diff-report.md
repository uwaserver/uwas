# Security Diff Report

**Branch:** main
**Base:** HEAD (working tree)
**Date:** 2026-07-10
**Files Changed:** 49
**Files Scanned:** 45 (4 documentation/generated report files filtered)

## Summary

| Category | New | Existing | Total |
|----------|-----|----------|-------|
| Critical | 0 | 0 | 0 |
| High | 0 | 0 | 0 |
| Medium | 0 | 0 | 0 |
| Low | 0 | 0 | 0 |

## Verdict

**PASS** - No security findings remain in the current diff.

## Remediated During Review

- GitHub Actions write permissions were moved from workflow scope to the specific Pages and release-publish jobs that require them.
- Persisted checkout credentials were disabled in every job.
- Release artifact builds no longer consume shared Go or npm caches.
- All first-party GitHub Actions were pinned to commit SHAs verified through the GitHub API.
- CI now enforces actionlint, zizmor, Hadolint, ShellCheck, and the 47-case Docker Compose integration suite with version/digest-pinned tooling and service images.
- The Docker builder and installed Alpine packages were pinned to verified versions; amd64 and arm64 builds both succeed.
- PHP, WordPress, and WP-CLI rebuild their pinned Alpine bases with the fixed `c-ares` package; MariaDB replaces its vulnerable upstream `gosu` helper with a reproducible Go 1.26.5 build.
- CI scans every produced runtime and integration image with Trivy; the final images contain no HIGH/CRITICAL vulnerabilities or detected secrets.
- WebSocket and mirror upstreams now use the same SSRF policy as ordinary proxy traffic, re-check the OS-selected IP at dial time to close DNS-rebinding races, and perform verified TLS handshakes for HTTPS/WSS backends.
- Proxy transport cache keys include dial, timeout, and TLS policy, and reload closes idle transports so tightened settings take effect immediately.
- HTTPS canary and sticky-affinity cookies are marked `Secure`; IP-hash affinity no longer includes ephemeral client ports.
- Trusted-proxy IPv6 addresses use canonical host/port formatting before downstream security middleware consumes them.
- Disk-cache operations are serialized, overwrite through an atomic rename, and store entries with owner-only permissions, preventing partial reads, local data exposure, and logical usage-accounting races.
- WordPress setup isolates Compose from ambient secret/config overrides, checks repeat-run domain consistency, and is exercised from a source archive in CI.
- Docker integration write requests retain the application's CSRF and JSON content-type protections instead of weakening production middleware.
- Docker admin ports are host-loopback-only, preventing API keys from crossing an unencrypted public listener; documentation uses SSH tunnels for remote access.
- The WordPress example now builds from source, passes required environment variables, uses digest-pinned WordPress/PHP/MariaDB images, and has a real HTTP/FastCGI integration gate covering `mysqli`, `wp-config.php`, WP-CLI, and the install page.

## Existing Observations

`GO-2026-5932` applies to `golang.org/x/crypto/openpgp`, an unmaintained package in the required module. The package is not imported or present in the dependency graph of any UWAS package. `govulncheck -show verbose` reports zero symbol and package vulnerabilities; there is no reachable finding to remediate.

## Dependency Changes

No Go or npm dependency manifests changed.

## Changed Files Not Scanned

- `CHANGELOG.md` - release history only
- `docs/IMPLEMENTATION.md` - documentation-only Go version alignment
- `security-report/SECURITY-REPORT.md` - resolved-status wording only
- `security-report/diff-report.md` - generated output of this scan

## Security Scan Results

**PASS**

**New findings:** 0
**Existing findings in touched files:** 0

No new security issues were detected in the final change set.
