FROM wordpress:cli-php8.3@sha256:94017f46151918dc886c2d6b3a072ce7912f9a935eb162566af184722a173a04

# The pinned upstream image ships vulnerable packages:
#   c-ares 1.34.6-r0              CVE-2026-33630
#   imagemagick-libs 7.1.2.24-r0  CVE-2026-53460, CVE-2026-53461
#   util-linux 2.42.1-r0          CVE-2026-53612…78410
#   libexpat 2.8.2/2.8.3-r0       CVE-2026-66046…93990
USER root
RUN apk add --no-cache --upgrade \
    "c-ares>=1.34.8-r0" \
    "imagemagick-libs>=7.1.2.27-r0" \
    "libexpat>=2.8.5-r0" \
    "util-linux>=2.42.3-r1"
USER www-data
