# ──────────────────────────────────────────────────────────────────────
# Mnemon Makefile
# ──────────────────────────────────────────────────────────────────────

BINARY      := mnemon
VERSION     ?= dev
LDFLAGS     := -s -w -X github.com/mnemon-dev/mnemon/cmd.version=$(VERSION)
GOBIN       := $(shell go env GOBIN)
ifeq ($(GOBIN),)
  GOBIN     := $(shell go env GOPATH)/bin
endif

.PHONY: deps build install uninstall test unit vet harness-validate codex-app-eval codex-app-eval-suite codex-memory-deep-eval codex-skill-deep-eval codex-eval-loop-smoke docker-build docker-run compose-up compose-down compose-dev release-snapshot clean help

.DEFAULT_GOAL := help

# ── Build ────────────────────────────────────────────────────────────

deps: ## Download Go dependencies
	go mod download

build: ## Build the mnemon binary
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# ── Install / Uninstall ─────────────────────────────────────────────

install: build ## Build and install mnemon to $GOBIN
	@mkdir -p $(GOBIN)
	cp $(BINARY) $(GOBIN)/$(BINARY)
	@echo "Installed: $(GOBIN)/$(BINARY)"

uninstall: ## Remove mnemon binary from $GOBIN
	rm -f $(GOBIN)/$(BINARY)
	@echo "Removed: $(GOBIN)/$(BINARY)"
	@echo "Run 'mnemon setup --eject' first to remove integrations."

# ── Test ─────────────────────────────────────────────────────────────

test: build ## Run E2E test suite
	bash scripts/e2e_test.sh

unit: ## Run Go unit tests
	go test ./...

vet: ## Run go vet static analysis
	go vet ./...

harness-validate: ## Validate harness module manifests and declared asset paths
	bash scripts/validate_harness_modules.sh

codex-app-eval: ## Run real Codex app-server harness smoke eval
	python3 scripts/codex_app_server_eval.py

codex-app-eval-suite: ## Run real Codex app-server memory/skill scenario suite
	python3 scripts/codex_app_server_eval.py --suite

codex-memory-deep-eval: ## Run deep real Codex app-server memory regression suite
	python3 scripts/codex_app_server_eval.py --suite --suite-name memory-deep

codex-skill-deep-eval: ## Run deep real Codex app-server skill regression suite
	python3 scripts/codex_app_server_eval.py --suite --suite-name skill-deep

codex-eval-loop-smoke: ## Run real Codex app-server eval-loop projection smoke check
	python3 scripts/codex_app_server_eval.py --module eval-loop

# ── Containers / Deployment ──────────────────────────────────────────

docker-build: ## Build runtime Docker image
	docker build --target runtime --build-arg VERSION=$(VERSION) -t mnemon-dev/mnemon:$(VERSION) .

docker-run: ## Run mnemon status in Docker with local .env
	docker run --rm --env-file .env -v mnemon-data:/data mnemon-dev/mnemon:$(VERSION) status

compose-up: ## Start mnemon with Docker Compose
	docker compose up -d mnemon

compose-down: ## Stop Docker Compose services
	docker compose down

compose-dev: ## Open a development shell in Docker Compose
	docker compose --profile dev run --rm mnemon-dev

release-snapshot: ## Build local GoReleaser snapshot artifacts
	goreleaser release --snapshot --clean

# ── Clean ────────────────────────────────────────────────────────────

clean: ## Remove build artifacts and test data
	rm -f $(BINARY)
	rm -rf .testdata

# ── Help ─────────────────────────────────────────────────────────────

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
