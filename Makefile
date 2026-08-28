GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/qubered/beacon/internal/buildinfo.Version=$(VERSION) \
           -X github.com/qubered/beacon/internal/buildinfo.Commit=$(COMMIT) \
           -X github.com/qubered/beacon/internal/buildinfo.Date=$(DATE)
COMPOSE := docker compose -f deploy/compose/docker-compose.yml

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build all binaries into ./bin
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

.PHONY: test
test: ## Run unit tests
	$(GO) test ./...

.PHONY: fmt
fmt: ## Format Go sources
	gofmt -w ./cmd ./internal ./pkg ./test

.PHONY: lint
lint: ## Static analysis
	$(GO) vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run || echo "golangci-lint not installed, skipping"

.PHONY: rules
rules: ## Enforce the repository rules CI enforces
	./tools/lint/vendorcheck.sh

.PHONY: check
check: fmt lint test rules ## Everything CI runs

.PHONY: dev
dev: ## Postgres + Core + Grafana on localhost
	$(COMPOSE) --profile monitoring up --build

.PHONY: dev-db
dev-db: ## Just Postgres
	$(COMPOSE) up -d postgres

.PHONY: migrate
migrate: ## Apply migrations to the dev database
	$(GO) run ./cmd/beaconctl migrate

.PHONY: down
down: ## Tear the dev stack down
	$(COMPOSE) --profile monitoring --profile fleet down

.PHONY: clean
clean:
	rm -rf bin web/dist
