# Build stage
FROM golang:1.26-alpine3.24@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none

RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w \
      -X 'github.com/uwaserver/uwas/internal/build.Version=${VERSION}' \
      -X 'github.com/uwaserver/uwas/internal/build.Commit=${COMMIT}' \
      -X 'github.com/uwaserver/uwas/internal/build.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
    -o /uwas ./cmd/uwas

# Runtime stage
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# ca-certificates: TLS verification for ACME, webhook delivery, DNS providers
# tzdata: correct timestamps in logs across deployments
# libcap: grants CAP_NET_BIND_SERVICE to the binary so the non-root user can
#         bind privileged ports (80/443). CGO_ENABLED=0 produces a static
#         binary that ignores setuid, so the capability must live on the file.
RUN apk add --no-cache ca-certificates tzdata libcap

COPY --from=builder /uwas /usr/local/bin/uwas

# Allow binding privileged ports (80/443) without root. Must run before the
# binary is chowned below — setcap needs root ownership of the target file.
RUN setcap cap_net_bind_service=+ep /usr/local/bin/uwas

# Non-root user: a compromised container never inherits host-equivalent root.
RUN addgroup -S uwas && adduser -S -D -H -G uwas uwas

# Pre-create runtime directories and hand ownership to the non-root user so it
# can write config, certs (ACME issuance), cache, logs, and the web root.
# /run is the real path behind the /var/run symlink (Alpine), so it must be
# chowned directly — `chown /var/run` would only touch the symlink, not the
# target, leaving the default pid_file (/var/run/uwas.pid) unwritable.
RUN mkdir -p /etc/uwas /var/lib/uwas/certs /var/cache/uwas /var/log/uwas /var/www /run && \
    chown -R uwas:uwas /etc/uwas /var/lib/uwas /var/cache/uwas /var/log/uwas /var/www /run

# Container entrypoint: seeds the config volume from this baked default on
# first boot, then execs the binary. Lets the named volume at /etc/uwas hold
# domain additions (domains.d/*.yaml) and config edits across restarts.
COPY docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY docker/uwas.yaml /etc/uwas.default/uwas.yaml
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

USER uwas

EXPOSE 80 443 9443

# Liveness/readiness probe against the public admin health endpoint.
# /api/v1/health requires no auth and reports subsystem status. start-period
# covers ACME cert issuance and middleware-chain warm-up on first boot.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:9443/api/v1/health || exit 1

# Entrypoint seeds the config volume on first boot, then runs the binary.
# CMD passes the serve subcommand + config path as args to the entrypoint.
ENTRYPOINT ["docker-entrypoint.sh", "uwas"]
CMD ["serve", "-c", "/etc/uwas/uwas.yaml"]
