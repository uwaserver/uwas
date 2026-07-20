FROM php:8.3-fpm-alpine@sha256:9fcec48321d890240d700ccdc2b475420c87d398826e68c3d8830b8fca663e5c

# The pinned upstream image contains c-ares 1.34.6-r0 (CVE-2026-33630).
RUN apk add --no-cache --upgrade c-ares=1.34.8-r0
