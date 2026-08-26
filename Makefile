# samsara-components Makefile
#
# Targets:
#   make test              — unit tests only (no Docker required)
#   make test-race         — unit tests with race detector
#   make fmt-check         — fail on any gofmt drift across all modules
#   make vet               — go vet across all modules
#   make lint              — staticcheck across all modules
#   make coverage          — unit tests with coverage report
#   make check             — fmt-check + vet + lint + test-race (run before pushing)
#   make infra-up          — start Docker Compose services
#   make infra-down        — stop and remove containers
#   make test-integration  — start infra, run integration tests, stop infra
#   make test-all          — unit + integration
#   make tidy              — go mod tidy across all modules

MODULES := $(shell find . -name go.mod -not -path './.git/*' | xargs -I{} dirname {})

INTEGRATION_TIMEOUT ?= 120s
UNIT_TIMEOUT        ?= 60s
COUNT               ?= 3

# ── Static analysis ───────────────────────────────────────────────────────────

.PHONY: fmt-check
fmt-check:
	@drift=$$(gofmt -l $(MODULES)); \
	if [ -n "$$drift" ]; then \
		echo "▶ gofmt drift:"; \
		echo "$$drift" | sed 's/^/  /'; \
		echo "run 'make fmt' to fix"; \
		exit 1; \
	fi
	@echo "▶ gofmt: clean"

.PHONY: fmt
fmt:
	@gofmt -w $(MODULES)

.PHONY: vet
vet:
	@for mod in $(MODULES); do \
		echo "▶ vet: $$mod"; \
		(cd $$mod && go vet ./...); \
	done

.PHONY: lint
lint:
	@which staticcheck > /dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	@for mod in $(MODULES); do \
		echo "▶ staticcheck: $$mod"; \
		(cd $$mod && staticcheck ./...); \
	done

.PHONY: check
check: fmt-check vet lint test-race

# ── Unit tests ────────────────────────────────────────────────────────────────

.PHONY: test
test:
	@for mod in $(MODULES); do \
		echo "▶ unit: $$mod"; \
		(cd $$mod && go test -timeout=$(UNIT_TIMEOUT) -count=$(COUNT) ./...); \
	done

.PHONY: test-race
test-race:
	@for mod in $(MODULES); do \
		echo "▶ unit -race: $$mod"; \
		(cd $$mod && go test -race -timeout=$(UNIT_TIMEOUT) -count=$(COUNT) ./...); \
	done

.PHONY: coverage
coverage:
	@for mod in $(MODULES); do \
		echo "▶ coverage: $$mod"; \
		(cd $$mod && go test -coverprofile=coverage.out -covermode=atomic ./... && \
			go tool cover -func=coverage.out | tail -1); \
	done

# ── Integration tests ─────────────────────────────────────────────────────────

.PHONY: infra-up
infra-up:
	docker compose up -d --wait
	docker compose --profile init run -d --rm seaweedfs-init
	@echo "✓ infrastructure ready"

.PHONY: infra-down
infra-down:
	docker compose down --volumes --remove-orphans

.PHONY: test-integration
test-integration: infra-up
	@trap '$(MAKE) infra-down' EXIT; \
	for mod in $(MODULES); do \
		echo "▶ integration -race: $$mod"; \
		(cd $$mod && go test -race -timeout=$(INTEGRATION_TIMEOUT) -count=1 \
			-tags integration ./...); \
	done

.PHONY: test-all
test-all: test-race test-integration

# ── Tidy ──────────────────────────────────────────────────────────────────────

.PHONY: tidy
tidy:
	@for mod in $(MODULES); do \
		echo "▶ tidy: $$mod"; \
		(cd $$mod && go mod tidy); \
	done
