# UWAS Multi-Domain Configuration

Split domain configs into separate files for cleaner management.

## Directory Structure

```
/etc/uwas/
├── uwas.yaml                     ← global settings only
└── domains.d/                    ← one file per domain
    ├── example.com.yaml          ← static site
    ├── blog.example.com.yaml     ← WordPress
    └── api.example.com.yaml      ← reverse proxy
```

## How It Works

UWAS automatically loads all `.yaml` files from `domains.d/` next to the main config. No need to list domains in the main config.

Each domain file is a single YAML document with the domain config:

```yaml
# domains.d/mysite.com.yaml
host: mysite.com
root: /var/www/mysite
type: static
ssl:
  mode: auto
```

## Adding a New Domain

```bash
# Create the domain config
cat > /etc/uwas/domains.d/newsite.com.yaml << 'EOF'
host: newsite.com
root: /var/www/newsite
type: static
ssl:
  mode: auto
EOF

# Reload UWAS (zero downtime)
kill -HUP $(pidof uwas)
# or: curl -X POST -H "Authorization: Bearer KEY" http://localhost:9443/api/v1/reload
```

## Alternative: Include Patterns

```yaml
# uwas.yaml
global:
  # ...
include:
  - "sites-enabled/*.yaml"    # glob pattern
  - "/opt/apps/*/uwas.yaml"   # absolute paths work too
```

## Alternative: Explicit Directory

```yaml
# uwas.yaml
global:
  # ...
domains_dir: /etc/uwas/sites-available
```

## Per-Domain File Formats

### Format 1: Single Domain (recommended for domains.d/)

```yaml
host: example.com
root: /var/www/example
type: static
ssl:
  mode: auto
```

### Format 2: Domain List (for include files)

```yaml
domains:
  - host: a.com
    root: /var/www/a
    type: static
  - host: b.com
    root: /var/www/b
    type: static
```

## Environment Variables

All domain files support `${VAR}` expansion:

```yaml
host: "${DOMAIN}"
root: "${WEB_ROOT:-/var/www/default}"
php:
  fpm_address: "tcp:${PHP_HOST:-127.0.0.1}:9000"
```
