FROM wordpress:php8.3-fpm-alpine@sha256:99ec55b49811ceecdac8c4ac6cc1bc95f61d564e9769cb598f6498a817047a61

# The pinned upstream image ships two vulnerable package sets:
#   c-ares 1.34.6-r0       CVE-2026-33630
#   ImageMagick 7.1.2.24-r0  CVE-2026-53460, CVE-2026-53461
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
    "imagemagick>=7.1.2.27-r0" \
    "imagemagick-jp2>=7.1.2.27-r0" \
    "imagemagick-jpeg>=7.1.2.27-r0" \
    "imagemagick-libs>=7.1.2.27-r0" \
    "imagemagick-pdf>=7.1.2.27-r0" \
    "imagemagick-tiff>=7.1.2.27-r0" \
    "imagemagick-webp>=7.1.2.27-r0"
