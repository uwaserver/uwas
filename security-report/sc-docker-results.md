# Docker & CI/CD Security Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-docker + sc-ci-cd
**Status:** Manual verification

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 0 |
| MEDIUM | 2 |
| LOW | 2 |
| **Total** | **4** |

## MEDIUM Findings

### MEDIUM-1: Dockerfile Uses Root in Runtime Stage
- **File:** `Dockerfile`
- **Line:** 23-34
- **CWE:** CWE-250 (Execution with Unnecessary Privileges)
- **Description:** The runtime stage does not create a non-root user. The `uwas` binary runs as root inside the container.
- **Recommendation:** Add `USER nobody` or create a dedicated `uwas` user in the runtime stage.

### MEDIUM-2: CI Workflow Uploads Test Output as Artifact
- **File:** `.github/workflows/ci.yml`
- **Line:** 28-33
- **CWE:** CWE-532 (Insertion of Sensitive Information into Log File)
- **Description:** Test output is uploaded as an artifact. If tests log sensitive data, it could be exposed.
- **Recommendation:** Sanitize test logs before artifact upload or restrict artifact retention.

## LOW Findings

### LOW-1: Dockerfile Base Image Not Pinned to Digest
- **File:** `Dockerfile`
- **CWE:** CWE-1104 (Use of Unmaintained Third-Party Components)
- **Description:** Uses `golang:1.26-alpine` and `alpine:3.19` tags which can change. Should pin to digest for reproducibility.
- **Recommendation:** Pin base images to SHA256 digest.

### LOW-2: Release Workflow Permissions Overly Broad
- **File:** `.github/workflows/release.yml`
- **CWE:** CWE-250
- **Description:** `permissions: contents: write` is broad. Could be narrowed.
- **Recommendation:** Use minimal required permissions.
