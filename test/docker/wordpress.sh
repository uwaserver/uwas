#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
INSTALL_DIR=$(mktemp -d)
ARCHIVE=$(mktemp --suffix=.tar.gz)
RESPONSE_FILE=$(mktemp)
SETUP_LOG=$(mktemp)
MISMATCH_LOG=$(mktemp)
PROJECT_NAME="uwas-wordpress-test-$$"
STACK_DIR="$INSTALL_DIR/source-test/examples/wordpress"
COMPOSE_FILE="$STACK_DIR/docker-compose.yml"
ENV_FILE="$STACK_DIR/.env.local"

cleanup() {
    if [[ -f "$COMPOSE_FILE" && -f "$ENV_FILE" ]]; then
        docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" \
            -f "$COMPOSE_FILE" down --volumes >/dev/null 2>&1 || true
    fi
    rm -rf "$INSTALL_DIR" "$ARCHIVE" "$RESPONSE_FILE" "$SETUP_LOG" "$MISMATCH_LOG"
}
trap cleanup EXIT

# Build the same top-level archive layout GitHub serves, while including local
# untracked additions during development and excluding deleted tracked files.
while IFS= read -r -d '' path; do
    if [[ -e "$ROOT_DIR/$path" || -L "$ROOT_DIR/$path" ]]; then
        printf '%s\0' "$path"
    fi
done < <(git -C "$ROOT_DIR" ls-files -co --exclude-standard -z) | \
    tar -C "$ROOT_DIR" --null -czf "$ARCHIVE" \
        --transform 's,^,uwas-test/,' -T -

SETUP_ENV=(
    UWAS_REF=test
    UWAS_INSTALL_DIR="$INSTALL_DIR"
    UWAS_ARCHIVE_URL="file://$ARCHIVE"
    UWAS_HTTP_PORT=18080
    UWAS_HTTPS_PORT=18443
    UWAS_ADMIN_PORT=19443
    UWAS_SSL_MODE=off
    UWAS_COMPOSE_PROJECT="$PROJECT_NAME"
)

env "${SETUP_ENV[@]}" bash "$ROOT_DIR/examples/wordpress/setup.sh" \
    wordpress.test >"$SETUP_LOG"
grep -q 'WordPress: http://wordpress.test:18080' "$SETUP_LOG"
grep -q 'Dashboard URL after tunneling: http://127.0.0.1:19443' "$SETUP_LOG"
[[ "$(stat -c '%a' "$ENV_FILE")" == "600" ]]

COMPOSE=(
    docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE"
    -f "$COMPOSE_FILE"
)

wordpress_status=$(curl --silent --output "$RESPONSE_FILE" \
    --write-out '%{http_code}' -H 'Host: wordpress.test' \
    http://127.0.0.1:18080/)
[[ "$wordpress_status" == "302" ]] || {
    "${COMPOSE[@]}" logs uwas php >&2
    printf 'WordPress root returned HTTP %s, want 302\n' "$wordpress_status" >&2
    exit 1
}

curl --fail --silent --show-error -H 'Host: wordpress.test' \
    http://127.0.0.1:18080/wp-admin/install.php >"$RESPONSE_FILE"
grep -q '<title>WordPress' "$RESPONSE_FILE"

"${COMPOSE[@]}" exec -T php test -f /var/www/html/wp-config.php
"${COMPOSE[@]}" exec -T php \
    php -r 'exit(extension_loaded("mysqli") ? 0 : 1);'
"${COMPOSE[@]}" --profile tools run --rm wp-cli core version \
    --path=/var/www/html >/dev/null

admin_binding=$("${COMPOSE[@]}" port uwas 9443)
[[ "$admin_binding" == "127.0.0.1:19443" ]] || {
    printf 'admin port binding is %s, want 127.0.0.1:19443\n' "$admin_binding" >&2
    exit 1
}

# Repeat with hostile ambient values: .env.local must remain authoritative.
env "${SETUP_ENV[@]}" DOMAIN=attacker.test HTTP_PORT=1 \
    DB_PASSWORD=ambient-secret bash "$ROOT_DIR/examples/wordpress/setup.sh" \
    wordpress.test >"$SETUP_LOG"
grep -q 'WordPress: http://wordpress.test:18080' "$SETUP_LOG"

set +e
env "${SETUP_ENV[@]}" bash "$ROOT_DIR/examples/wordpress/setup.sh" \
    other.test >"$MISMATCH_LOG" 2>&1
mismatch_status=$?
set -e
[[ "$mismatch_status" -ne 0 ]]
grep -q 'configured for wordpress.test, not other.test' "$MISMATCH_LOG"

printf 'WordPress setup and integration checks passed.\n'
