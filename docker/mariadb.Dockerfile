FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine3.24@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS gosu-builder

ARG TARGETARCH
ARG TARGETOS

# MariaDB's gosu 1.19 binary was built with vulnerable Go 1.24.6. Rebuild the
# same audited commit with the project's patched Go toolchain.
RUN CGO_ENABLED=0 GOARCH=$TARGETARCH GOOS=$TARGETOS \
    go install -ldflags="-s -w" \
    github.com/tianon/gosu@6456aaa0f3c854d199d0f037f068eb97515b7513

FROM mariadb:11@sha256:efb4959ef2c835cd735dbc388eb9ad6aab0c78dd64febcd51bc17481111890c4

COPY --from=gosu-builder --chmod=0755 /go/bin/gosu /usr/local/bin/gosu
