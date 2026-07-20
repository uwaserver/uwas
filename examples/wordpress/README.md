# UWAS + WordPress — Production Deployment

Deploy a complete WordPress site on any VPS with Docker Compose. The example builds UWAS from the cloned repository and pins every third-party service image by digest.

## What You Get

- **UWAS** web server with auto HTTPS (Let's Encrypt)
- **PHP-FPM 8.3** with OPcache enabled
- **MariaDB 11** with performance tuning
- **Automatic WordPress** download and configuration
- **Built-in caching** (memory + disk, wp-admin bypassed)
- **Security hardened** (WAF, rate limiting, blocked paths, xmlrpc disabled)
- **Pretty permalinks** via .htaccess support
- **Admin dashboard** on host loopback, accessed through an SSH tunnel

## Quick Start (VPS)

### 1. Install Docker

Install Docker Engine and the Compose v2 plugin using the
[official Docker instructions](https://docs.docker.com/engine/install/), then
verify both `docker --version` and `docker compose version`. The setup script
deliberately does not pipe a remote installer into a privileged shell.

### 2. Clone and configure

```bash
git clone https://github.com/uwaserver/uwas.git
cd uwas/examples/wordpress

# Edit your settings
cp .env.example .env.local
nano .env.local
```

Required changes in `.env.local`:

```ini
DOMAIN=yourdomain.com
ADMIN_EMAIL=you@yourdomain.com
DB_ROOT_PASSWORD=your_secure_root_password
DB_PASSWORD=your_secure_db_password
UWAS_ADMIN_KEY=your_secure_admin_key
```

Optional values include `DB_NAME`, `DB_USER`, `HTTP_PORT`, `HTTPS_PORT`,
`ADMIN_PORT`, and `SSL_MODE` (`auto` or `off`). The compose file reads
`.env.local` via `--env-file`, so keep secrets out of committed files.

### 3. Point DNS

Create an A record pointing `yourdomain.com` to your VPS IP.
If using `www`, also create an A record for `www.yourdomain.com`.

### 4. Deploy

```bash
docker compose --env-file .env.local up -d --build
```

Wait for MariaDB and the official WordPress FPM image to initialize the shared
volume. `docker compose --env-file .env.local ps` should show `db`, `php`, and
`uwas` as healthy.

### 5. Install WordPress

Open `https://yourdomain.com` in your browser and complete the WordPress setup wizard.

## Management

### UWAS Dashboard

The default admin listener is HTTP and is not exposed to the network. Create an
SSH tunnel from your workstation:

```bash
ssh -L 9443:127.0.0.1:9443 user@yourdomain.com
```

Then open `http://127.0.0.1:9443/_uwas/dashboard/` and use the
`UWAS_ADMIN_KEY` from `.env.local`.

Features: live stats, domain management, cache control, access logs, server metrics, and the full UWAS admin API.

### Common Commands

```bash
# View logs
docker compose --env-file .env.local logs -f uwas
docker compose --env-file .env.local logs -f php

# Restart services
docker compose --env-file .env.local restart uwas
docker compose --env-file .env.local restart php

# Update WordPress (via WP-CLI)
docker compose --env-file .env.local --profile tools run --rm wp-cli core update

# Purge cache
curl -X POST -H "Authorization: Bearer YOUR_KEY" \
  -H "X-Requested-With: XMLHttpRequest" \
  -H "Content-Type: application/json" -d '{}' \
  http://127.0.0.1:9443/api/v1/cache/purge

# Backup database
docker compose --env-file .env.local exec db mariadb-dump -u root -p wordpress > backup.sql

# Restore database
docker compose --env-file .env.local exec -i db mariadb -u root -p wordpress < backup.sql

# Backup WordPress files
docker compose --env-file .env.local cp php:/var/www/html ./backup/
```

### File Access

WordPress files are stored in the `wordpress` Docker volume. To access:

```bash
# List files
docker compose --env-file .env.local exec php ls -la /var/www/html/

# Edit wp-config.php
docker compose --env-file .env.local exec php vi /var/www/html/wp-config.php

# Copy files from host
docker compose --env-file .env.local cp ./my-theme.zip php:/var/www/html/wp-content/themes/

# Copy files to host
docker compose --env-file .env.local cp php:/var/www/html/wp-content/uploads ./uploads-backup/
```

## Architecture

```
Internet
  │
  ▼
┌─────────────────────────────────────┐
│  UWAS (:80/:443)                    │
│  ├── Auto HTTPS (Let's Encrypt)     │
│  ├── Static file serving            │
│  ├── .htaccess rewrite engine       │
│  ├── Cache (memory + disk)          │
│  ├── WAF + rate limiting            │
│  └── Admin dashboard (:9443, local) │
└──────────┬──────────────────────────┘
           │ FastCGI (tcp:9000)
           ▼
┌──────────────────────┐
│  PHP-FPM 8.3         │
│  ├── OPcache enabled │
│  ├── 256MB memory    │
│  └── 64MB upload     │
└──────────┬───────────┘
           │ MySQL protocol
           ▼
┌──────────────────────┐
│  MariaDB 11          │
│  ├── 256MB buffer    │
│  ├── Query cache     │
│  └── UTF-8mb4        │
└──────────────────────┘
```

## Volumes

| Volume | Purpose | Backup? |
|--------|---------|---------|
| `wordpress` | WordPress core + plugins + themes | Yes |
| `uploads` | Media uploads (images, files) | Yes |
| `db_data` | MariaDB data | Yes (critical) |
| `certs` | TLS certificates | Auto-renewed |
| `cache` | UWAS cache | No (regenerated) |
| `logs` | Access logs | Optional |

## Security Notes

- `wp-config.php` is blocked from direct HTTP access
- `xmlrpc.php` is blocked (common attack target)
- WAF blocks SQL injection, XSS, and path traversal
- Rate limiting: 60 requests/minute per IP
- Admin dashboard requires API key
- HTTPS enforced with HSTS
- PHP `expose_php` disabled
- MariaDB not exposed to the internet (backend network only)

## Customization

### Add a second site

Edit `config/uwas.yaml` and add another domain block:

```yaml
domains:
  - host: "blog.example.com"
    # ... (copy the WordPress block and change domain/root)
  - host: "shop.example.com"
    root: /var/www/shop
    type: php
    # ...
```

### Use Redis for object caching

Add to `docker-compose.yml`:

```yaml
  redis:
    image: redis:7-alpine@sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99
    restart: unless-stopped
    networks:
      - backend
```

Then install the Redis Object Cache WordPress plugin.

### Custom PHP extensions

The pinned official WordPress FPM image already includes the database and media
extensions required by WordPress. For additional extensions, derive a custom
image from the exact pinned digest in `docker-compose.yml`, pin any Alpine build
packages, and replace the `php.image` field with a local `build` block.

## Troubleshooting

| Issue | Solution |
|-------|----------|
| "Error establishing database connection" | Wait for MariaDB to become healthy; check `DB_PASSWORD` in `.env.local` |
| HTTPS not working | Ensure DNS A record points to your VPS, port 80 must be open |
| File upload fails | Check `upload_max_filesize` in config/php.ini |
| 502 Bad Gateway | Check PHP-FPM: `docker compose --env-file .env.local logs php` |
| Slow pages | Check cache: `curl -I https://yourdomain.com` → look for `X-Cache: HIT` |
