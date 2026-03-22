# UWAS + WordPress — Production Deployment

Deploy a complete WordPress site on any VPS with a single command.

## What You Get

- **UWAS** web server with auto HTTPS (Let's Encrypt)
- **PHP-FPM 8.3** with OPcache enabled
- **MariaDB 11** with performance tuning
- **Automatic WordPress** download and configuration
- **Built-in caching** (memory + disk, wp-admin bypassed)
- **Security hardened** (WAF, rate limiting, blocked paths, xmlrpc disabled)
- **Pretty permalinks** via .htaccess support
- **Admin dashboard** at `https://yourdomain.com:9443/_uwas/dashboard/`

## Quick Start (VPS)

### 1. Install Docker

```bash
curl -fsSL https://get.docker.com | sh
```

### 2. Clone and configure

```bash
git clone https://github.com/uwaserver/uwas.git
cd uwas/examples/wordpress

# Edit your settings
cp .env .env.local
nano .env.local
```

Required changes in `.env`:

```ini
DOMAIN=yourdomain.com
ADMIN_EMAIL=you@yourdomain.com
DB_ROOT_PASSWORD=your_secure_root_password
DB_PASSWORD=your_secure_db_password
UWAS_ADMIN_KEY=your_secure_admin_key
```

### 3. Point DNS

Create an A record pointing `yourdomain.com` to your VPS IP.
If using `www`, also create an A record for `www.yourdomain.com`.

### 4. Deploy

```bash
docker compose --env-file .env.local up -d
```

Wait ~30 seconds for WordPress to download and database to initialize.

### 5. Install WordPress

Open `https://yourdomain.com` in your browser and complete the WordPress setup wizard.

## Management

### UWAS Dashboard

```
https://yourdomain.com:9443/_uwas/dashboard/
API Key: your UWAS_ADMIN_KEY from .env
```

Features: live stats, domain management, cache control, access logs, server metrics.

### Common Commands

```bash
# View logs
docker compose logs -f uwas
docker compose logs -f php

# Restart services
docker compose restart uwas
docker compose restart php

# Update WordPress (via WP-CLI)
docker compose run --rm wp-init wp core update --allow-root

# Purge cache
curl -X POST -H "Authorization: Bearer YOUR_KEY" \
  https://yourdomain.com:9443/api/v1/cache/purge

# Backup database
docker compose exec db mariadb-dump -u root -p wordpress > backup.sql

# Restore database
docker compose exec -i db mariadb -u root -p wordpress < backup.sql

# Backup WordPress files
docker compose cp php:/var/www/wordpress ./backup/
```

### File Access

WordPress files are stored in the `wordpress` Docker volume. To access:

```bash
# List files
docker compose exec php ls -la /var/www/wordpress/

# Edit wp-config.php
docker compose exec php vi /var/www/wordpress/wp-config.php

# Copy files from host
docker compose cp ./my-theme.zip php:/var/www/wordpress/wp-content/themes/

# Copy files to host
docker compose cp php:/var/www/wordpress/wp-content/uploads ./uploads-backup/
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
│  └── Admin dashboard (:9443)        │
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
    image: redis:7-alpine
    restart: unless-stopped
    networks:
      - backend
```

Then install the Redis Object Cache WordPress plugin.

### Custom PHP extensions

Create a custom PHP Dockerfile:

```dockerfile
FROM php:8.3-fpm-alpine
RUN docker-php-ext-install mysqli pdo_mysql
RUN apk add --no-cache libzip-dev && docker-php-ext-install zip
RUN docker-php-ext-install gd
```

## Troubleshooting

| Issue | Solution |
|-------|----------|
| "Error establishing database connection" | Wait 30s for MariaDB to start, check DB_PASSWORD in .env |
| HTTPS not working | Ensure DNS A record points to your VPS, port 80 must be open |
| File upload fails | Check `upload_max_filesize` in config/php.ini |
| 502 Bad Gateway | Check PHP-FPM: `docker compose logs php` |
| Slow pages | Check cache: `curl -I https://yourdomain.com` → look for `X-Cache: HIT` |
