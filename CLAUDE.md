# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`samsara-components` is a Go workspace monorepo of lifecycle-managed infrastructure components for the [samsara](https://github.com/sunkek/samsara) runtime. Each component implements the samsara lifecycle interface (`Start`, `Stop`, `Health`) and is an independent Go module.

**Modules:** `fiber/`, `grpc/`, `grpcclient/`, `postgresql/`, `rabbitmq/`, `redis/`, `s3/`

## Commands

```bash
make check             # go vet + staticcheck + race-tested unit tests — run before pushing
make test              # unit tests only
make test-race         # unit tests with race detector
make test-integration  # start Docker, run integration tests, stop Docker
make test-all          # unit + integration
make coverage          # per-module coverage summaries
make tidy              # go mod tidy across all modules
```

To run tests for a single module:
```bash
cd postgresql && go test -race -count=3 ./...
cd postgresql && go test -race -count=1 -tags integration ./...
```

`staticcheck` is auto-installed by `make lint` if missing.

## Architecture

All components follow the same internal layout:

```
<module>/
├── <module>.go           # Component struct + Start/Stop/Health lifecycle
├── config.go             # Config struct with defaults
├── client.go             # Public API (queries, pub/sub ops, etc.)
├── *_test.go             # Unit tests
└── *_integration_test.go # Integration tests (//go:build integration)
```

Integration tests require real services provided by `docker-compose.yml` (PostgreSQL, Redis, RabbitMQ, SeaweedFS for S3-compatible storage).

## Conventions

- **Scope:** Do not edit unrelated modules just because they share the workspace.
- **Errors:** Wrap with context: `fmt.Errorf("operation: %w", err)`.
- **Exports:** All exported identifiers require Go doc comments.
- **Tests:** Unit tests cover lifecycle and config logic; integration tests cover real network behavior.
- **Commits:** Short imperative messages scoped to module or behavior, e.g. `postgresql: add WithName option`.
- Use `make` targets rather than ad hoc `go` commands so behavior stays consistent with CI.
