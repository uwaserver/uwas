# sc-secrets results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Summary: No hardcoded production secrets, private keys, or service tokens were found in the UWAS source; the only finding is a weak default DB password baked into the example `docker-compose.yml` fallback.

## Methodology

Scanned `.go`, `.ts/.tsx`, `.yaml/.yml`, `.json`, `.env*`, `.sh`, `Dockerfile*`, and `.github/workflows/*` for:
- Known key prefixes (AWS `AKIA`, GitHub `ghp_/ghs_`, GitLab `glpat-`, Stripe `sk_live_`, Slack `xox*`, Google `AIza`, npm/PyPI tokens) — **none found**.
- PEM private-key blocks (`BEGIN ... PRIVATE KEY`) — **none found** in source, certs, or examples.
- Generic `password|secret|token|api_key|private_key = "literal"` assignments — only struct field declarations (`json:`/`yaml:` tags) and runtime env reads, not literals.
- Connection strings with embedded credentials (`mysql://user:pass@`, etc.) — **none found**.
- Long base64 / high-entropy literals and hardcoded `[]byte("...")` crypto keys — **none found**.
- Fallback-to-constant-secret patterns (`if secret == "" { secret = "..." }`) — **none found**.

## Defenses observed (good)

- **JWT signing key** (`internal/auth/persist.go:26-62`) is generated with CSPRNG and persisted to disk on first use; there is no hardcoded fallback secret. Load requires `len >= 32`.
- **API key auth** uses `crypto/subtle.ConstantTimeCompare` (`internal/auth/manager.go:413`, `internal/admin/api.go:435`); per-user keys are stored as SHA-256 hashes (`hashAPIKey`), not plaintext.
- **TOTP/HMAC** use proper random keys, not literals (`internal/admin/totp.go`, `handlers_apps_webhook.go`).
- **Settings/log masking**: secret-bearing fields (`api_key`, `totp_secret`, `password`, `secret_key`, `pin_code`) are masked in the settings GET API (`internal/admin/handlers_settings.go:265`) and redacted in access logs (`internal/middleware/accesslog.go:16`).
- **.gitignore** excludes real `.env` (`/.env`, allows only `!.env.example`); no `.env`, `.pem`, `.key`, or `id_rsa` files are tracked by git.
- Install scripts (`install.sh`, `scripts/install.sh`, `docker/entrypoint.sh`) do not embed admin keys.
- `docker/uwas.yaml` and `uwas.example.yaml` read the admin key from `${UWAS_ADMIN_KEY}` (env), not a literal.

## Findings

### SECRET-001: Weak default database password in docker-compose fallback
- **Severity:** Low
- **Confidence:** 45
- **File:** `docker-compose.yml:19,39,42`
- **CWE:** CWE-1392 (Use of Default Credentials) / CWE-798
- **Evidence:**
  ```yaml
  - UWAS_DB_PASSWORD=${DB_ROOT_PASSWORD:-uwas_root}
  ...
  MARIADB_ROOT_PASSWORD: ${DB_ROOT_PASSWORD:-uwas_root}
  MARIADB_PASSWORD: ${DB_PASSWORD:-uwas_wp}
  ```
- **Why it could matter:** If an operator runs `docker compose up` without first setting `DB_ROOT_PASSWORD`/`DB_PASSWORD` (or copying `.env.example` and editing it), MariaDB starts with the publicly known constant root password `uwas_root` and app password `uwas_wp`. If the DB port is ever exposed, this is trivially guessable.
- **Mitigating factors:** The DB service is not port-mapped to the host in this compose file (no `ports:` on `db`), so it is only reachable on the internal compose network by default; the value is an overridable env default, not a secret embedded in the shipped binary; and `.env.example` documents that real values must be set. This is a deployment-hygiene weak-default, not a leaked credential.
- **Remediation:** Remove the `:-uwas_root` / `:-uwas_wp` fallbacks so compose fails fast when the env vars are unset, or generate a random password at first boot. Document that `DB_ROOT_PASSWORD`/`DB_PASSWORD` are mandatory.

## Excluded (verified false positives)

- `.env.example`, `examples/wordpress/.env`, `test/e2e/uwas-e2e.yaml`, `test/docker/*` — all placeholder/test values (`please-change-this-admin-key`, `change_me_*`, `test-key-12345`, `e2e-test-key`). Config templates / test fixtures per skill FP rules.
- `.github/workflows/ci.yml` (`MARIADB_ROOT_PASSWORD: root`, `UWAS_DB_PASSWORD: "root"`) — CI-only throwaway test DB, not shipped.
- `internal/admin/handlers_software_*.go` (`N8N_BASIC_AUTH_USER: "admin"`, `MINIO_ROOT_USER: "admin"`) — default *usernames* with passwords sourced from request/env, not hardcoded secrets.
