# Running UWAS in Docker

This guide covers the Docker deployment: first boot, customizing the config,
and managing the volumes. For the high-level feature list and CLI, see the
[root README](../README.md).

## Quick start

```bash
cp .env.example .env       # then edit .env and set UWAS_ADMIN_KEY
docker compose up -d
```

The dashboard is at `https://<host>:9443/_uwas/dashboard/` (or `http://` if
TLS is not yet configured). Log in with the admin API key you set.

## What the image does for you

- **Runs as a non-root user** (`uwas`, with `CAP_NET_BIND_SERVICE` so it can
  bind ports 80/443).
- **Seeds the config volume on first boot.** The entrypoint copies the baked
  default config (`/etc/uwas.default/uwas.yaml`) into the empty config volume
  at `/etc/uwas/uwas.yaml`, then starts the server. On later boots the volume
  already holds a config and the copy is skipped — your edits are preserved.
- **Exposes a healthcheck** against `/api/v1/health` (no auth required).
- **Persists state across restarts** via named volumes (config, certs, cache).
- **Reports its runtime in the dashboard.** The UWAS card on the main dashboard
  shows the container type and non-root status (e.g. `docker · non-root`) so
  you can confirm the hardening is active at a glance. This comes from
  `/api/v1/system` (`container` and `non_root` fields).

## Configuration

The admin API binds `:9443` inside the container and **requires an API key** —
UWAS refuses to start a publicly-bound admin listener without authentication.

### Set the admin key

Set `UWAS_ADMIN_KEY` in `.env` (loaded automatically by compose):

```bash
cp .env.example .env
# generate a strong key
echo "UWAS_ADMIN_KEY=$(openssl rand -hex 24)" >> .env
```

This key is read by `docker/uwas.yaml` via `${UWAS_ADMIN_KEY}` env expansion
and is what you'll use to log in to the dashboard and CLI.

### Customize the config before first boot

The volume is seeded from `docker/uwas.yaml` (baked into the image). To change
the defaults (listener ports, cache size, ACME email, sample domain, etc.),
edit `docker/uwas.yaml` **before** the first `docker compose up`. Once the
container has started, the seeded copy in the volume is the source of truth and
changes to `docker/uwas.yaml` will not be picked up (see "Reseed" below).

To customize after first boot, use the **Settings** or **Config Editor** pages
in the dashboard — they write directly to the volume.

### Reseed the config volume

If you want to start fresh (e.g. after changing `docker/uwas.yaml`), remove the
volume so the entrypoint seeds it again on next boot:

```bash
docker compose down
docker volume rm uwas_uwas_config      # exact name depends on the project dir
docker compose up -d
```

> ⚠️ This discards all config, domains, and credentials stored in the volume.
> Back up first if needed.

## Volumes

| Volume | Mount | Contents |
|--------|-------|----------|
| `uwas_config` | `/etc/uwas` | Main config (`uwas.yaml`), `domains.d/*.yaml`, audit state, Cloudflare state |
| `certs` | `/var/lib/uwas/certs` | TLS certificates (ACME-issued and manual) |
| `cache` | `/var/cache/uwas` | On-disk page cache (L2) |
| `db_data` | `/var/lib/mysql` | MariaDB data (compose `db` service) |

Web roots are bind-mounted from `./data/www` on the host (so you can edit site
files directly); everything else uses named volumes for portability.

Inspect a volume:

```bash
docker run --rm -v uwas_uwas_config:/data alpine ls -la /data
```

Back up the config volume:

```bash
docker run --rm -v uwas_uwas_config:/data -v "$PWD":/backup alpine \
  tar czf /backup/uwas-config.tar.gz -C /data .
```

## Ports

| Port | Protocol | Purpose |
|------|----------|---------|
| 80 | TCP | HTTP (redirects to HTTPS, serves ACME challenges) |
| 443 | TCP | HTTPS (TLS termination) |
| 443 | UDP | HTTP/3 (QUIC) |
| 9443 | TCP | Admin API + dashboard |

## Using a database (MariaDB)

The compose file includes a `db` service (MariaDB 11). UWAS auto-detects it via
the `MARIADB_*` environment variables. Set the root password and app password
in `.env`:

```
DB_ROOT_PASSWORD=<strong-password>
DB_PASSWORD=<strong-password>
```

To run UWAS without the bundled database (e.g. you have an external MySQL),
remove the `db` service and `depends_on` from `docker-compose.yml`.

## Using PHP

The compose file includes a `php` service (PHP 8.3 FPM). To route a domain to
it, set the domain type to `php` and point `php.fpm_address` at the service:

```yaml
domains:
  - host: blog.example.com
    type: php
    root: /var/www/blog
    php:
      fpm_address: "tcp://php:9000"
```

## Standalone `docker run` (without compose)

```bash
docker build -t uwas .
docker run -d -p 80:80 -p 443:443 -p 9443:9443 \
  -e UWAS_ADMIN_KEY=your-admin-key \
  -v uwas_config:/etc/uwas \
  uwas
```

You lose the PHP and database sidecars — add them as separate containers and
point UWAS at them in the config.

## Troubleshooting

### Container exits immediately
Check the logs — the most common cause is a missing or empty `UWAS_ADMIN_KEY`:
UWAS rejects a publicly-bound admin listener without auth.

```bash
docker compose logs uwas
```

### "failed to write pid file: permission denied"
This was fixed by giving the `uwas` user ownership of `/run`. If you see it on
a custom image, ensure the runtime user can write the `pid_file` path
(default `/var/run/uwas.pid`).

### Healthcheck stays "starting"
The healthcheck hits `http://127.0.0.1:9443/api/v1/health`. If it never goes
healthy, the admin server isn't starting — check the logs for config or bind
errors.

### Domains added from the dashboard disappear on restart
This happens if the config is bind-mounted read-only (`-v ./uwas.yaml:/etc/uwas/uwas.yaml:ro`)
instead of using the `uwas_config` named volume. The volume is required for
persistence — see [Volumes](#volumes).

### Verifying the runtime environment
The dashboard UWAS card shows `docker · non-root` when running correctly in a
container. To verify from the command line instead:

```bash
docker exec <container> id                # uid should not be 0
docker exec <container> wget -qO- http://127.0.0.1:9443/api/v1/system | grep -o '"container":"[^"]*"'
```

If `container` reports `"none"`, the container detection heuristics
(`/.dockerenv`, `/proc/1/cgroup`) did not match — this can happen on niche
runtimes. The UWAS binary still runs correctly; only the dashboard label is
affected.

### Running tests against a container
The Go test suite runs in parallel by default — Docker-based integration tests
use unique ports and PID-scoped container names, so no `-p 1` serial flag is
needed. To run the full suite:

```bash
docker exec <container> uwas doctor    # quick sanity check (no Docker daemon inside the container)
```

For local development, tests run on the host (not inside the container) since
they need Docker daemon access for integration tests:

```bash
make test    # parallel, ~4 min locally
```
