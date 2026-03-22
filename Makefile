VERSION ?= $(shell git describe --tags 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BINARY  := uwas

LDFLAGS := -s -w \
	-X 'github.com/uwaserver/uwas/internal/build.Version=$(VERSION)' \
	-X 'github.com/uwaserver/uwas/internal/build.Commit=$(COMMIT)' \
	-X 'github.com/uwaserver/uwas/internal/build.Date=$(DATE)'

.PHONY: build dev test test-coverage lint clean run dashboard

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/uwas
	@echo "Built bin/$(BINARY) $(VERSION)"

dev:
	go build -o bin/$(BINARY) ./cmd/uwas

run: dev
	./bin/$(BINARY) serve -c uwas.example.yaml

test:
	go test -count=1 -timeout 300s ./...

test-coverage:
	go test ./internal/... ./pkg/... -coverprofile=coverage.out -timeout 300s
	@go tool cover -func=coverage.out | grep "^total:" | awk '{print "Total coverage:", $$3}'

lint:
	go vet ./...
	@which staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"

dashboard:
	cd web/dashboard && npm run build

clean:
	rm -rf bin/ coverage.out
	go clean -testcache
