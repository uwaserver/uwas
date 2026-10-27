FROM php:8.3.36-fpm-alpine@sha256:3ea3c3f8ee03269b77f5c2bda5879b44ba152cb13aafead8b67c4332f829aa2d

# The pinned upstream image contains c-ares 1.34.6-r0 (CVE-2026-33630) and
# brotli-libs 1.2.0-r1 (CVE-2026-111497) and curl/libcurl 8.22.0-r0
# (CVE-2026-92392).
RUN apk add --no-cache --upgrade "c-ares>=1.34.8-r0" "brotli-libs>=1.2.1-r0" \
    "curl>=8.23.0-r0" "libcurl>=8.23.0-r0"
