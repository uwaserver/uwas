VERSION ?= $(shell git describe --tags 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BINARY  := uwas

LDFLAGS := -s -w \
	-X 'github.com/uwaserver/uwas/internal/build.Version=$(VERSION)' \
	-X 'github.com/uwaserver/uwas/internal/build.Commit=$(COMMIT)' \
	-X 'github.com/uwaserver/uwas/internal/build.Date=$(DATE)'

.PHONY: build dev test test-coverage lint clean run dashboard release check all

# ── Development ─────────────────────────────────────────────

dev:
	go build -o bin/$(BINARY) ./cmd/uwas

run: dev
	./bin/$(BINARY) serve -c uwas.example.yaml

# ── Production ──────────────────────────────────────────────

build: dashboard
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/uwas
	@echo "Built bin/$(BINARY) $(VERSION)"

linux: dashboard
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/uwas
	@echo "Built bin/$(BINARY)-linux-amd64 $(VERSION)"

linux-arm: dashboard
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 ./cmd/uwas
	@echo "Built bin/$(BINARY)-linux-arm64 $(VERSION)"

release: check dashboard linux linux-arm
	@echo "Release binaries ready in bin/"
	@ls -lh bin/$(BINARY)-linux-*

# ── Quality ─────────────────────────────────────────────────

test:
	go test -count=1 -timeout 600s ./...

test-coverage:
	go test ./internal/... ./pkg/... -coverprofile=coverage.out -timeout 600s
	@go tool cover -func=coverage.out | grep "^total:" | awk '{print "Total coverage:", $$3}'

lint:
	go vet ./...
	@which staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"

check: lint
	@echo "=== Go vet ===" && go vet ./...
	@echo "=== TypeScript ===" && cd web/dashboard && npx tsc -b
	@echo "=== Tests ===" && go test -count=1 -timeout 600s ./...
	@echo "All checks passed."

# ── Dashboard ───────────────────────────────────────────────

dashboard:
	cd web/dashboard && npm run build
	@echo "Dashboard built and embedded"

dashboard-dev:
	cd web/dashboard && npm run dev

# ── Utility ─────────────────────────────────────────────────

clean:
	rm -rf bin/ *.out
	go clean -testcache

all: check build
	@echo "Build complete: bin/$(BINARY) $(VERSION)"

# ── Deploy (requires SSH_HOST env var) ──────────────────────

deploy: linux
	@if [ -z "$(SSH_HOST)" ]; then echo "Set SSH_HOST=user@host"; exit 1; fi
	scp bin/$(BINARY)-linux-amd64 $(SSH_HOST):/tmp/uwas-new
	ssh $(SSH_HOST) 'sudo /tmp/uwas-new stop 2>/dev/null; sudo mv /tmp/uwas-new /usr/local/bin/uwas; sudo chmod +x /usr/local/bin/uwas; sudo uwas serve -d && echo "Deployed and started"'
