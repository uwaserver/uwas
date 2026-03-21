VERSION ?= $(shell git describe --tags 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BINARY  := uwas

LDFLAGS := -s -w \
	-X 'github.com/uwaserver/uwas/internal/build.Version=$(VERSION)' \
	-X 'github.com/uwaserver/uwas/internal/build.Commit=$(COMMIT)' \
	-X 'github.com/uwaserver/uwas/internal/build.Date=$(DATE)'

.PHONY: build dev test lint clean run

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/uwas

dev:
	go build -o bin/$(BINARY) ./cmd/uwas

run: dev
	./bin/$(BINARY) serve -c uwas.example.yaml

test:
	go test -race -count=1 ./...

lint:
	go vet ./...
	@which staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"

clean:
	rm -rf bin/
	go clean -testcache
