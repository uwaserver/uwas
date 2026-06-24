#!/bin/sh
# UWAS container entrypoint.
#
# Seeds the config volume on first boot, then execs the real binary.
#
# Why: docker-compose.yml mounts a named volume at /etc/uwas so the dashboard
# can persist domain additions (domains.d/*.yaml) and config edits. An empty
# named volume would leave UWAS without a config, and its built-in first-run
# flow is interactive (prompts for ports/paths) — unsuitable for a container.
# This script copies the image-baked default config into the volume once, then
# hands control to the binary. On subsequent boots the volume already holds a
# config and the copy is skipped (no clobbering of operator changes).
set -e

CONF_DIR="/etc/uwas"
CONF_FILE="${CONF_DIR}/uwas.yaml"
DEFAULT_CONF="/etc/uwas.default/uwas.yaml"

# Seed the config only when it is missing. `-f` (not `-e`) on purpose: a
# broken/zero-byte file is still the operator's file and must not be silently
# overwritten by the baked default.
if [ ! -f "${CONF_FILE}" ]; then
    if [ -f "${DEFAULT_CONF}" ]; then
        echo "[entrypoint] seeding config from image default -> ${CONF_FILE}"
        cp "${DEFAULT_CONF}" "${CONF_FILE}"
    else
        echo "[entrypoint] no config at ${CONF_FILE} and no image default; starting with UWAS defaults" >&2
    fi
fi

# domains.d/ must exist for domain CRUD; the admin server creates it lazily,
# but pre-creating avoids a per-add directory stat under volume mounts.
mkdir -p "${CONF_DIR}/domains.d" 2>/dev/null || true

exec "$@"
