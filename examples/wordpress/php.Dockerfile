FROM wordpress:php8.3-fpm-alpine@sha256:d0d4f306d3bc343f5429356e085d0bde8fbd048ebac33f41a26d2ed6edc5df95

# The pinned upstream image ships vulnerable package sets:
#   c-ares 1.34.6-r0         CVE-2026-33630
#   ImageMagick 7.1.2.24-r0  CVE-2026-53460, CVE-2026-53461
#   util-linux 2.42.1-r0     CVE-2026-53612…78410
#   libexpat 2.8.2/2.8.3-r0  CVE-2026-66046…93990
# The imagemagick sub-packages are listed together because apk refuses a
# partial upgrade across a shared origin.
#
# Version floors, not exact pins. An exact pin says "this build is only valid
# while Alpine still publishes this exact -r0", and Alpine rotates old
# revisions out — so the build breaks on a schedule nobody controls, which it
# already has once. A floor keeps the thing the pin was for (never install
# something older than the fix) and survives the rotation.
RUN apk add --no-cache --upgrade \
    "c-ares>=1.34.8-r0" \
    "brotli-libs>=1.2.1-r0" \
    "curl>=8.23.0-r0" \
    "libcurl>=8.23.0-r0" \
    "imagemagick>=7.1.2.27-r0" \
    "imagemagick-jp2>=7.1.2.27-r0" \
    "imagemagick-jpeg>=7.1.2.27-r0" \
    "imagemagick-libs>=7.1.2.27-r0" \
    "imagemagick-pdf>=7.1.2.27-r0" \
    "imagemagick-tiff>=7.1.2.27-r0" \
    "imagemagick-webp>=7.1.2.27-r0" \
    "libexpat>=2.8.5-r0" \
    "util-linux>=2.42.3-r1"
