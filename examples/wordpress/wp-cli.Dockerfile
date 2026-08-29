FROM wordpress:cli-php8.3@sha256:f8aeb68164c6a04f5dcc91da30d8ffa096b0f7fafb7a65f144c2dd62587caca0

# The pinned upstream image ships two vulnerable packages:
#   c-ares 1.34.6-r0              CVE-2026-33630
#   imagemagick-libs 7.1.2.24-r0  CVE-2026-53460, CVE-2026-53461
USER root
RUN apk add --no-cache --upgrade \
    "c-ares>=1.34.8-r0" \
    "imagemagick-libs>=7.1.2.27-r0"
USER www-data
