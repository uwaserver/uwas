FROM php:8.3.36-fpm-alpine@sha256:3ea3c3f8ee03269b77f5c2bda5879b44ba152cb13aafead8b67c4332f829aa2d

# The pinned upstream image contains c-ares 1.34.6-r0 (CVE-2026-33630) and
# brotli-libs 1.2.0-r1 (CVE-2026-111497) and curl/libcurl 8.22.0-r0
# (CVE-2026-92392), libxml2 2.13.9-r2 (CVE-2026-86144), musl 1.2.6-r2
# (CVE-2026-107891) and zlib 1.3.2-r0 (CVE-2026-85091).
RUN apk add --no-cache --upgrade "c-ares>=1.34.8-r0" "brotli-libs>=1.2.1-r0" \
    "curl>=8.23.0-r0" "libcurl>=8.23.0-r0" "libxml2>=2.13.9-r3" \
    "musl>=1.2.6-r3" "musl-utils>=1.2.6-r3" "zlib>=1.3.2-r1"
