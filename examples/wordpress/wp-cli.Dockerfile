FROM wordpress:cli-php8.3@sha256:94017f46151918dc886c2d6b3a072ce7912f9a935eb162566af184722a173a04

# The pinned upstream image ships vulnerable packages:
#   c-ares 1.34.6-r0              CVE-2026-33630
#   imagemagick-libs 7.1.2.24-r0  CVE-2026-53460, CVE-2026-53461
#   util-linux 2.42.1-r0          CVE-2026-53612…78410
#   libexpat 2.8.2/2.8.3-r0       CVE-2026-66046…93990; 2.9.0-r0 CVE-2026-113351
#   libxml2 2.13.9-r2             CVE-2026-86144
#   mariadb-client 11.8.9-r0      CVE-2026-101679
#   musl 1.2.6-r2                 CVE-2026-107891
#   zlib 1.3.2-r0                 CVE-2026-85091
USER root
RUN apk add --no-cache --upgrade \
    "c-ares>=1.34.8-r0" \
    "brotli-libs>=1.2.1-r0" \
    "curl>=8.23.0-r0" \
    "libcurl>=8.23.0-r0" \
    "imagemagick-libs>=7.1.2.27-r0" \
    "libexpat>=2.10.0-r0" \
    "libxml2>=2.13.9-r3" \
    "mariadb-client>=11.8.10-r0" \
    "mariadb-common>=11.8.10-r0" \
    "mysql-client>=11.8.10-r0" \
    "musl>=1.2.6-r3" \
    "musl-utils>=1.2.6-r3" \
    "zlib>=1.3.2-r1" \
    "util-linux>=2.42.3-r1"
USER www-data
