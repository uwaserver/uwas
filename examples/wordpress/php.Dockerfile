FROM wordpress:php8.3-fpm-alpine@sha256:99ec55b49811ceecdac8c4ac6cc1bc95f61d564e9769cb598f6498a817047a61

# The pinned upstream image contains c-ares 1.34.6-r0 (CVE-2026-33630).
RUN apk add --no-cache --upgrade c-ares=1.34.8-r0
