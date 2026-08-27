# samsara-components Makefile
#
# Targets:
#   make test              — unit tests only (no Docker required)
#   make test-race         — unit tests with race detector
#   make fmt-check         — fail on any gofmt drift across all modules
#   make vet               — go vet across all modules
#   make lint              — staticcheck across all modules
#   make vuln              — govulncheck across all modules
#   make coverage          — unit tests with coverage report
#   make coverage-check    — fail if any module drops below its recorded baseline
#   make coverage-update   — rewrite the baseline's unit column
#   make coverage-update-integration — rewrite both columns (needs Docker)
#   make check             — fmt-check + vet + lint + test-race (run before pushing)
#   make infra-up          — start Docker Compose services
#   make infra-down        — stop and remove containers
#   make test-integration  — start infra, run integration tests, stop infra
#   make test-all          — unit + integration
#   make tidy              — go mod tidy across all modules

MODULES := $(shell find . -name go.mod -not -path './.git/*' | xargs -I{} dirname {})

# Tool versions are pinned so CI and local runs agree, and so a new upstream
# release cannot break the build on its own schedule. `go run pkg@version`
# resolves through the module cache, which means no install step and no stale
# binary already on PATH.
STATICCHECK         ?= honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOVULNCHECK         ?= golang.org/x/vuln/cmd/govulncheck@v1.7.0

COVERAGE_BASELINE   ?= scripts/coverage-baseline.txt
COVERAGE_TOLERANCE  ?= 2.0

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
	@for mod in $(MODULES); do \
		echo "▶ staticcheck: $$mod"; \
		(cd $$mod && go run $(STATICCHECK) ./...); \
	done

# govulncheck fails only on advisories whose vulnerable symbols this code calls;
# an advisory in a required-but-uncalled module is reported without failing.
.PHONY: vuln
vuln:
	@for mod in $(MODULES); do \
		echo "▶ govulncheck: $$mod"; \
		(cd $$mod && go run $(GOVULNCHECK) ./...); \
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

.PHONY: coverage-check
coverage-check:
	@fail=0; \
	for mod in $(MODULES); do \
		pct=$$(cd $$mod && go test -coverprofile=coverage.out -covermode=atomic ./... > /dev/null && \
			go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
		base=$$(grep "^$$mod " $(COVERAGE_BASELINE) | awk '{print $$2}'); \
		if [ -z "$$base" ]; then \
			echo "▶ coverage: $$mod $$pct% — no baseline; run 'make coverage-update'"; \
			fail=1; \
			continue; \
		fi; \
		if awk "BEGIN{exit !($$pct < $$base - $(COVERAGE_TOLERANCE))}"; then \
			echo "▶ coverage: $$mod $$pct% — DOWN from baseline $$base%"; \
			fail=1; \
		else \
			echo "▶ coverage: $$mod $$pct% (baseline $$base%)"; \
		fi; \
	done; \
	if [ $$fail -ne 0 ]; then \
		echo "coverage dropped more than $(COVERAGE_TOLERANCE) points; add tests or run 'make coverage-update'"; \
		exit 1; \
	fi

.PHONY: coverage-update
coverage-update:
	@tmp=$$(mktemp); \
	sed -n '/^#/p' $(COVERAGE_BASELINE) > $$tmp; \
	for mod in $$(echo $(MODULES) | tr ' ' '\n' | sort); do \
		pct=$$(cd $$mod && go test -coverprofile=coverage.out -covermode=atomic ./... > /dev/null && \
			go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
		integ=$$(grep "^$$mod " $(COVERAGE_BASELINE) | awk '{print $$3}'); \
		echo "$$mod $$pct $$integ" >> $$tmp; \
	done; \
	mv $$tmp $(COVERAGE_BASELINE); \
	echo "▶ baseline written to $(COVERAGE_BASELINE) (unit column)"

# Both columns. Needs the docker-compose services up; run behind infra-up, or
# use `make infra-up && make coverage-update-integration && make infra-down`.
.PHONY: coverage-update-integration
coverage-update-integration:
	@tmp=$$(mktemp); \
	sed -n '/^#/p' $(COVERAGE_BASELINE) > $$tmp; \
	for mod in $$(echo $(MODULES) | tr ' ' '\n' | sort); do \
		pct=$$(cd $$mod && go test -coverprofile=coverage.out -covermode=atomic ./... > /dev/null && \
			go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
		integ=$$(cd $$mod && go test -tags integration -timeout=$(INTEGRATION_TIMEOUT) \
			-coverprofile=coverage.out -covermode=atomic ./... > /dev/null && \
			go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
		echo "$$mod $$pct $$integ" >> $$tmp; \
	done; \
	mv $$tmp $(COVERAGE_BASELINE); \
	echo "▶ baseline written to $(COVERAGE_BASELINE) (both columns)"

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
