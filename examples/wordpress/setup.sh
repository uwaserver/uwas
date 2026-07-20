#!/usr/bin/env bash
set -euo pipefail

# UWAS + WordPress setup from a repository archive.
# Usage: sudo bash setup.sh example.com

readonly REPOSITORY="uwaserver/uwas"
readonly DOMAIN="${1:-}"
readonly UWAS_REF="${UWAS_REF:-main}"
readonly INSTALL_DIR="${UWAS_INSTALL_DIR:-/opt/uwas-wordpress}"
readonly ARCHIVE_URL="${UWAS_ARCHIVE_URL:-https://github.com/$REPOSITORY/archive/$UWAS_REF.tar.gz}"
readonly HTTP_PORT="${UWAS_HTTP_PORT:-80}"
readonly HTTPS_PORT="${UWAS_HTTPS_PORT:-443}"
readonly ADMIN_PORT="${UWAS_ADMIN_PORT:-9443}"
readonly SSL_MODE="${UWAS_SSL_MODE:-auto}"
readonly PROJECT_NAME="${UWAS_COMPOSE_PROJECT:-uwas-wordpress}"

die() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

if [[ -z "$DOMAIN" ]]; then
    die "usage: $0 <domain>"
fi
if [[ ! "$DOMAIN" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$ ]]; then
    die "invalid domain: $DOMAIN"
fi
if [[ ! "$UWAS_REF" =~ ^[A-Za-z0-9._-]+$ ]]; then
    die "UWAS_REF must be a branch or tag name without slashes"
fi
for port in "$HTTP_PORT" "$HTTPS_PORT" "$ADMIN_PORT"; do
    if [[ ! "$port" =~ ^[0-9]+$ ]] || ((10#$port < 1 || 10#$port > 65535)); then
        die "invalid TCP port: $port"
    fi
done
if [[ "$SSL_MODE" != "auto" && "$SSL_MODE" != "off" ]]; then
    die "UWAS_SSL_MODE must be auto or off"
fi
if [[ ! "$PROJECT_NAME" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
    die "UWAS_COMPOSE_PROJECT must contain only lowercase letters, digits, hyphens, and underscores"
fi

for command in curl docker env openssl tar; do
    command -v "$command" >/dev/null 2>&1 || die "required command not found: $command"
done
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required"

readonly SOURCE_DIR="$INSTALL_DIR/source-$UWAS_REF"
readonly STACK_DIR="$SOURCE_DIR/examples/wordpress"

if [[ ! -f "$SOURCE_DIR/Dockerfile" ]]; then
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    archive="$temp_dir/uwas.tar.gz"

    printf 'Downloading UWAS source (%s)...\n' "$UWAS_REF"
    curl --fail --silent --show-error --location \
        "$ARCHIVE_URL" \
        --output "$archive"

    mkdir -p "$SOURCE_DIR"
    tar -xzf "$archive" --strip-components=1 -C "$SOURCE_DIR"
fi

[[ -f "$STACK_DIR/docker-compose.yml" ]] || die "WordPress stack not found in downloaded source"
cd "$STACK_DIR"

if [[ ! -f .env.local ]]; then
    umask 077
    db_root_password=$(openssl rand -hex 24)
    db_password=$(openssl rand -hex 24)
    admin_key=$(openssl rand -hex 24)

    cat >.env.local <<EOF
DOMAIN=$DOMAIN
ADMIN_EMAIL=admin@$DOMAIN
DB_ROOT_PASSWORD=$db_root_password
DB_NAME=wordpress
DB_USER=wordpress
DB_PASSWORD=$db_password
UWAS_ADMIN_KEY=$admin_key
HTTP_PORT=$HTTP_PORT
HTTPS_PORT=$HTTPS_PORT
ADMIN_PORT=$ADMIN_PORT
SSL_MODE=$SSL_MODE
EOF
    chmod 600 .env.local
else
    printf 'Using existing %s/.env.local\n' "$STACK_DIR"
fi
chmod 600 .env.local

# The env file is authoritative on repeat runs. Remove ambient copies of stack
# variables so an exported shell variable cannot silently change the deployment.
COMPOSE=(
    env -u DOMAIN -u ADMIN_EMAIL -u DB_ROOT_PASSWORD -u DB_NAME -u DB_USER
    -u DB_PASSWORD -u UWAS_ADMIN_KEY -u HTTP_PORT -u HTTPS_PORT -u ADMIN_PORT
    -u SSL_MODE -u COMPOSE_PROJECT_NAME docker compose
    --project-name "$PROJECT_NAME" --env-file .env.local
)

compose_env_value() {
    local key=$1
    "${COMPOSE[@]}" config --environment | awk -v prefix="$key=" '
        index($0, prefix) == 1 { value = substr($0, length(prefix) + 1) }
        END { if (value == "") exit 1; print value }
    '
}

published_port() {
    local endpoint port
    endpoint=$("${COMPOSE[@]}" port uwas "$1")
    port=${endpoint##*:}
    [[ "$port" =~ ^[0-9]+$ ]] || die "could not determine the published port for container port $1"
    printf '%s\n' "$port"
}

"${COMPOSE[@]}" config --quiet
effective_domain=$(compose_env_value DOMAIN) || die "DOMAIN is missing from .env.local"
effective_ssl_mode=$(compose_env_value SSL_MODE) || die "SSL_MODE is missing from .env.local"
[[ "$effective_domain" == "$DOMAIN" ]] || \
    die "existing .env.local is configured for $effective_domain, not $DOMAIN"
[[ "$effective_ssl_mode" == "auto" || "$effective_ssl_mode" == "off" ]] || \
    die "existing .env.local has invalid SSL_MODE: $effective_ssl_mode"

"${COMPOSE[@]}" up -d --build

wordpress_ready=false
for ((attempt = 0; attempt < 180; attempt++)); do
	if "${COMPOSE[@]}" exec -T php \
	    test -f /var/www/html/wp-config.php >/dev/null 2>&1 && \
	    "${COMPOSE[@]}" exec -T php \
	    php -r 'exit(extension_loaded("mysqli") ? 0 : 1);' >/dev/null 2>&1; then
        wordpress_ready=true
        break
    fi
    sleep 1
done
if [[ "$wordpress_ready" != "true" ]]; then
	"${COMPOSE[@]}" logs php >&2
    die "WordPress initialization timed out"
fi

http_port=$(published_port 80)
https_port=$(published_port 443)
admin_port=$(published_port 9443)

admin_ready=false
for ((attempt = 0; attempt < 60; attempt++)); do
    if curl --fail --silent --output /dev/null "http://127.0.0.1:$admin_port/api/v1/health"; then
        admin_ready=true
        break
    fi
    sleep 1
done
if [[ "$admin_ready" != "true" ]]; then
	"${COMPOSE[@]}" logs uwas >&2
	die "UWAS admin health check timed out"
fi

curl --fail --silent --output /dev/null -H "Host: $effective_domain" \
	"http://127.0.0.1:$http_port/" || die "WordPress HTTP endpoint is not reachable through UWAS"

printf '\nSetup complete.\n'
if [[ "$effective_ssl_mode" == "auto" ]]; then
    if [[ "$https_port" == "443" ]]; then
        printf 'WordPress: https://%s\n' "$effective_domain"
    else
        printf 'WordPress: https://%s:%s\n' "$effective_domain" "$https_port"
    fi
else
    printf 'WordPress: http://%s:%s\n' "$effective_domain" "$http_port"
fi
printf 'Credentials: %s/.env.local (mode 0600)\n' "$STACK_DIR"
printf 'Dashboard tunnel: ssh -L %s:127.0.0.1:%s <user>@%s\n' "$admin_port" "$admin_port" "$effective_domain"
printf 'Dashboard URL after tunneling: http://127.0.0.1:%s/_uwas/dashboard/\n' "$admin_port"
