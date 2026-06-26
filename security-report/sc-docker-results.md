# sc-docker — Docker Security Scan Results

**Summary:** UWAS's Dockerfile is well-hardened (multi-stage, non-root USER, minimal `cap_net_bind_service`, healthcheck, COPY-not-ADD, exec-form ENTRYPOINT), but the default `docker-compose.yml` ships weak/guessable default credentials (admin API key and DB root password) and publishes the full admin control panel on all interfaces, and the compose services lack runtime hardening (no `no-new-privileges`, `cap_drop`, `read_only`, or resource limits).

---

## Finding DOCK-001: Publicly-known default admin API key in docker-compose.yml

- **Severity:** High
- **Confidence:** 72
- **File:** docker-compose.yml:13
- **CWE:** CWE-798 (Use of Hard-coded / Default Credentials)
- **Evidence:**
  ```yaml
  environment:
    - UWAS_ADMIN_KEY=${UWAS_ADMIN_KEY:-please-change-this-admin-key}
  ```
- **Why exploitable:** The compose file publishes the admin API on `9443:9443` (bound to `0.0.0.0`, DOCK-003). The baked `docker/uwas.yaml` sets `admin.listen: ":9443"` and `api_key: "${UWAS_ADMIN_KEY}"`, which `internal/config/loader.go:45` (`expandEnvVars`) substitutes from the container env. If the operator runs `docker compose up` without exporting `UWAS_ADMIN_KEY`, the admin listener comes up guarded by the publicly-known literal `please-change-this-admin-key`. The startup guard in `internal/admin/api.go:233` only refuses to boot when the key is *empty* on a non-loopback bind — a non-empty default key passes the check, so the server starts silently with a guessable credential protecting the entire 250+ endpoint control panel (domain CRUD, file manager, terminal/PTY, SFTP users, DB, firewall). An attacker who reaches port 9443 and sends the known key gains full host-level control of the panel.
- **Mitigation observed:** Inline comment warns "Override on the host or in a .env file; never commit a real key," and the default value is self-describing. Still, the deployment boots and works with the default, so following the instruction is not enforced.
- **Remediation:** Remove the `:-please-change-this-admin-key` fallback so `docker compose up` fails fast when `UWAS_ADMIN_KEY` is unset (`- UWAS_ADMIN_KEY=${UWAS_ADMIN_KEY:?set a strong admin key}`), or generate a random key in the entrypoint and print it once. Prefer Docker Compose `secrets:` over an env var.

---

## Finding DOCK-002: Default database root/user passwords in docker-compose.yml

- **Severity:** Medium
- **Confidence:** 70
- **File:** docker-compose.yml:19,39,42
- **CWE:** CWE-798 (Use of Hard-coded / Default Credentials)
- **Evidence:**
  ```yaml
  - UWAS_DB_PASSWORD=${DB_ROOT_PASSWORD:-uwas_root}
  ...
  MARIADB_ROOT_PASSWORD: ${DB_ROOT_PASSWORD:-uwas_root}
  MARIADB_PASSWORD: ${DB_PASSWORD:-uwas_wp}
  ```
- **Why exploitable:** The MariaDB container defaults to root password `uwas_root` and WordPress user password `uwas_wp` when the env vars are unset. The UWAS container also receives `UWAS_DB_USER=root` + the same default root password for backup-restore operations.
- **Mitigation observed:** The `db` service declares **no** `ports:`, so MariaDB is reachable only on the internal compose bridge network, not from the host — this is what holds the severity to Medium rather than High. Any other container sharing the network (e.g. the `php` service, or a future compromised service) can authenticate as DB root with the known default.
- **Remediation:** Use required-variable syntax (`${DB_ROOT_PASSWORD:?required}`) or Compose `secrets:`; never use the DB `root` account from the app — create a least-privilege user.

---

## Finding DOCK-003: Admin control-panel port published on all interfaces

- **Severity:** Medium
- **Confidence:** 60
- **File:** docker-compose.yml:7
- **CWE:** CWE-668 (Exposure of Resource to Wrong Sphere)
- **Evidence:**
  ```yaml
  ports:
    - "9443:9443"
  ```
- **Why exploitable:** A short `"9443:9443"` mapping binds to `0.0.0.0`, exposing the admin API to every host interface (and, depending on firewalling, the public internet). This is the network exposure that makes DOCK-001 reachable. Application ports 80/443 are expected to be public; the admin/control plane should not be.
- **Remediation:** Bind the admin port to loopback or a management interface: `- "127.0.0.1:9443:9443"`, and reach it via SSH tunnel or reverse proxy with auth.

---

## Finding DOCK-004: Compose services lack runtime hardening

- **Severity:** Low
- **Confidence:** 80
- **File:** docker-compose.yml:1-45
- **CWE:** CWE-250 (Execution with Unnecessary Privileges) / CWE-732 / CWE-400
- **Evidence:** None of the `uwas`, `php`, or `db` services set `security_opt: [no-new-privileges:true]`, `cap_drop: [ALL]`, `read_only: true`, `pids_limit`, or `deploy.resources.limits` (memory/CPU).
- **Why it matters:** Without `no-new-privileges` and `cap_drop`, a compromised process inside a container retains the default capability set and can escalate via setuid binaries. Without resource limits, a single container can exhaust host memory/CPU (DoS). The `uwas` image already runs as a non-root `USER`, which limits impact, hence Low.
- **Remediation:** Add to each service:
  ```yaml
  security_opt: ["no-new-privileges:true"]
  cap_drop: ["ALL"]
  cap_add: ["NET_BIND_SERVICE"]   # uwas only
  deploy:
    resources:
      limits: { memory: 512M, cpus: "1.0" }
  ```

---

## Finding DOCK-005: Base images not pinned by digest

- **Severity:** Low
- **Confidence:** 65
- **File:** Dockerfile:2,23
- **CWE:** CWE-829 (Inclusion of Functionality from Untrusted Control Sphere) / CWE-1104
- **Evidence:**
  ```dockerfile
  FROM golang:1.26-alpine3.24 AS builder
  ...
  FROM alpine:3.24
  ```
- **Why it matters:** Tags are version-specific (no `:latest`, good), but mutable — `alpine:3.24` can be re-pushed. Without `@sha256:...` digest pinning, builds are not reproducible and are exposed to upstream tag/registry tampering. Build cleanliness is otherwise good (no `ADD`, no piped `curl|sh`, no embedded secrets in the final stage; the build context is consumed only in the discarded builder stage).
- **Remediation:** Pin both `FROM` lines to digests (`FROM alpine:3.24@sha256:...`) and refresh via Dependabot/renovate.

---

## Finding DOCK-006: .dockerignore omits common secret patterns

- **Severity:** Low
- **Confidence:** 40
- **File:** .dockerignore:1-20
- **CWE:** CWE-200 (Exposure of Sensitive Information)
- **Evidence:** `.dockerignore` excludes `.git`, `bin/`, `node_modules/`, `*.log`, etc., but not `.env`, `*.key`, `*.pem`, or `*.yaml` config files containing secrets.
- **Why it matters / mitigation:** The Dockerfile uses `COPY . .` only in the **builder** stage; the final image copies just the compiled `/uwas` binary, the entrypoint, and `docker/uwas.yaml`. So secrets in the build context are not present in the published image — risk is limited to the builder layers / local build cache. Hence Low/Informational.
- **Remediation:** Add `.env`, `*.key`, `*.pem`, `*.crt`, and local `uwas*.yaml` (except the intended `docker/uwas.yaml`) to `.dockerignore` for defense in depth.

---

### Notes / defenses observed (no finding)
- Runtime container spawning (`internal/database/docker.go:78-97`, `internal/deploy/deploy.go:378-387`) binds host ports to `127.0.0.1` only, uses `--restart=unless-stopped`, and does **not** use `--privileged`, mount `/var/run/docker.sock`, or default to host networking — good. (`deploy.DockerNetwork` lets an authenticated admin opt into `--network host`, but that is an explicit operator choice, not a default.)
- Dockerfile runtime stage runs as non-root `USER uwas` with only `cap_net_bind_service`, includes a `HEALTHCHECK`, and uses exec-form `ENTRYPOINT`/`CMD`.
- The test compose (`test/docker/docker-compose.test.yml`) uses read-only mounts and an isolated bridge network; its credentials are test-only.
