FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine3.24@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS gosu-builder

ARG TARGETARCH
ARG TARGETOS

# MariaDB's gosu 1.19 binary was built with vulnerable Go 1.24.6. Rebuild the
# same audited commit with the project's patched Go toolchain.
RUN CGO_ENABLED=0 GOARCH=$TARGETARCH GOOS=$TARGETOS \
    go install -ldflags="-s -w" \
    github.com/tianon/gosu@6456aaa0f3c854d199d0f037f068eb97515b7513

FROM mariadb:11@sha256:efb4959ef2c835cd735dbc388eb9ad6aab0c78dd64febcd51bc17481111890c4

COPY --from=gosu-builder --chmod=0755 /go/bin/gosu /usr/local/bin/gosu
