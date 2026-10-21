FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine3.24@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca AS gosu-builder

ARG TARGETARCH
ARG TARGETOS

# MariaDB's gosu 1.19 binary was built with vulnerable Go 1.24.6. Rebuild the
# same audited commit with the project's patched Go toolchain.
RUN CGO_ENABLED=0 GOARCH=$TARGETARCH GOOS=$TARGETOS \
    go install -ldflags="-s -w" \
    github.com/tianon/gosu@6456aaa0f3c854d199d0f037f068eb97515b7513

FROM mariadb:11@sha256:efb4959ef2c835cd735dbc388eb9ad6aab0c78dd64febcd51bc17481111890c4

# The base image ships openssl 3.0.13-0ubuntu3.11; Trivy flags
# CVE-2026-84782 (HIGH). Pull the security-pocket fix while keeping the
# digest-pinned MariaDB layers for everything else.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libssl3t64=3.0.13-0ubuntu3.16 \
        openssl=3.0.13-0ubuntu3.16 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=gosu-builder --chmod=0755 /go/bin/gosu /usr/local/bin/gosu
