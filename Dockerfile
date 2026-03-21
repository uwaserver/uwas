# Build stage
FROM golang:1.26-alpine AS builder

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
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /uwas /usr/local/bin/uwas

RUN mkdir -p /etc/uwas /var/lib/uwas/certs /var/cache/uwas /var/log/uwas

EXPOSE 80 443 9443

ENTRYPOINT ["uwas"]
CMD ["serve", "-c", "/etc/uwas/uwas.yaml"]
