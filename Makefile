VERSION ?= $(shell git describe --tags 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BINARY  := uwas
GO_PACKAGES := $(shell go list ./... | grep -v '/node_modules/')

LDFLAGS := -s -w \
	-X 'github.com/uwaserver/uwas/internal/build.Version=$(VERSION)' \
	-X 'github.com/uwaserver/uwas/internal/build.Commit=$(COMMIT)' \
	-X 'github.com/uwaserver/uwas/internal/build.Date=$(DATE)'

.PHONY: help build dev test test-race test-coverage lint check clean run dashboard dashboard-dev release all deploy

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# -- Development -------------------------------------------------------------

dev: ## Build development binary (no version metadata)
	go build -o bin/$(BINARY) ./cmd/uwas

run: dev ## Build and run with example config
	./bin/$(BINARY) serve -c uwas.example.yaml

# -- Production ---------------------------------------------------------------

build: dashboard ## Build production binary with embedded dashboard
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/uwas
	@echo "Built bin/$(BINARY) $(VERSION)"

linux: dashboard ## Cross-compile for linux/amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/uwas
	@echo "Built bin/$(BINARY)-linux-amd64 $(VERSION)"

linux-arm: dashboard ## Cross-compile for linux/arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 ./cmd/uwas
	@echo "Built bin/$(BINARY)-linux-arm64 $(VERSION)"

release: check dashboard linux linux-arm ## Full release build (checks + cross-compile)
	@echo "Release binaries ready in bin/"
	@ls -lh bin/$(BINARY)-linux-*

# -- Quality -----------------------------------------------------------------

# Tests run in parallel by default (Docker tests use unique ports/PIDs).
# Use -p 1 only if you suspect cross-package interference.
test: ## Run all Go tests (race detector on)
	go test -race -count=1 -timeout 900s $(GO_PACKAGES)

# Race-detector gate, package-for-package identical to the CI `race` job
# (.github/workflows/ci.yml). Past races here (lazy-init, inflight-map delete,
# LRU/list interleavings, protocol framing) were invisible to the plain gate —
# they only reproduce under -race.
#
# The package set is derived with `go list ./...` rather than hand-listed on
# purpose: a hand-maintained list silently drifts from CI, and it had drifted
# badly — 63 of 73 packages were uncovered locally, including internal/phpmanager
# and internal/backup. Deriving it makes local and CI identical by construction
# and makes a newly added package race-checked from its first commit.
#
# Two notes on matching CI:
#   - CI uses `mapfile` + process substitution, which are bash-only. This file
#     sets no SHELL, so make runs recipes under /bin/sh (dash); the POSIX
#     command substitution below is the equivalent.
#   - CI additionally starts MariaDB and exports UWAS_DB_* so DB-backed tests
#     actually execute. Without a local database those paths skip, so this
#     target's coverage of DB paths is shallower than CI's even though the
#     package set now matches.
test-race: ## Run race-detector tests on all packages (same set as the CI race job)
	go test -race -count=1 -timeout 900s $$(go list ./... | grep -v '/node_modules/')

test-coverage: ## Run tests with coverage and print total
	go test ./internal/... ./pkg/... -coverprofile=coverage.out -timeout 600s
	@go tool cover -func=coverage.out | grep "^total:" | awk '{print "Total coverage:", $$3}'

lint: ## Run Go vet and staticcheck (if installed)
	go vet $(GO_PACKAGES)
	@which staticcheck >/dev/null 2>&1 && staticcheck $(GO_PACKAGES) || echo "staticcheck not installed — install with: go install honnef.co/go/tools/cmd/staticcheck@latest"

check: ## Full quality gate: vet + TypeScript + frontend tests + Go tests
	@echo "=== Go vet ===" && go vet $(GO_PACKAGES)
	@echo "=== TypeScript ===" && cd web/dashboard && npx tsc -b
	@echo "=== Frontend tests ===" && cd web/dashboard && npm test
	@echo "=== Go tests ===" && go test -count=1 -timeout 600s $(GO_PACKAGES)
	@echo "All checks passed."

# -- Dashboard ----------------------------------------------------------------

dashboard: ## Build dashboard SPA (embedded into binary)
	cd web/dashboard && npm run build
	@echo "Dashboard built and embedded"

dashboard-dev: ## Start dashboard dev server with hot reload
	cd web/dashboard && npm run dev

# -- Utility ------------------------------------------------------------------

clean: ## Remove build artifacts, coverage reports, and test cache
	rm -rf bin/ *.out *.html bench.test
	go clean -testcache

all: check build ## Run all checks then build
	@echo "Build complete: bin/$(BINARY) $(VERSION)"

# -- Deploy (requires SSH_HOST env var) ---------------------------------------

deploy: linux ## SCP binary to remote host and restart (SSH_HOST required)
	@if [ -z "$(SSH_HOST)" ]; then echo "Set SSH_HOST=user@host"; exit 1; fi
	scp bin/$(BINARY)-linux-amd64 $(SSH_HOST):/tmp/uwas-new
	ssh $(SSH_HOST) 'sudo /tmp/uwas-new stop 2>/dev/null; sudo mv /tmp/uwas-new /usr/local/bin/uwas; sudo chmod +x /usr/local/bin/uwas; sudo uwas serve -d && echo "Deployed and started"'
