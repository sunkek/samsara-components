# Contributing to samsara-components

Thank you for your interest in contributing. This document explains how to get
started, how the repository is structured, and what the quality bar is for
changes.

---

## Repository structure

This is a Go workspace monorepo. Each infrastructure component is an
independent module in its own subdirectory:

```
samsara-components/
├── go.work                  # workspace — ties all modules together locally
├── fiber/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/fiber
│   ├── fiber.go             # component lifecycle (Start, Stop, Health)
│   ├── routes.go            # Register / Use API
│   ├── errors.go            # DefaultErrorHandler, ErrorResponse, HTTPStatuser
│   ├── swagger.go           # WithSwagger option
│   ├── helpers.go           # RealIP, ExcludeRoutes, Route, SkipperFunc
│   ├── fiber_test.go              # unit tests (no server binding required)
│   └── fiber_integration_test.go  # integration tests (//go:build integration)
├── grpc/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/grpc
│   ├── grpc.go              # component lifecycle (Start, Stop, Health)
│   ├── config.go            # Config and keepalive helpers
│   ├── grpc_test.go              # unit tests (no server binding required)
│   └── grpc_integration_test.go  # integration tests (//go:build integration)
├── grpcclient/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/grpcclient
│   ├── grpcclient.go        # component lifecycle (Start, Stop, Health), Conn()
│   ├── config.go            # Config and dial option helpers
│   ├── grpcclient_test.go              # unit tests (no server required)
│   └── grpcclient_integration_test.go  # integration tests (//go:build integration)
├── postgresql/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/postgresql
│   ├── postgresql.go        # component lifecycle (Start, Stop, Health)
│   ├── db.go                # query API (Select, Get, Exec, transactions)
│   ├── postgresql_test.go              # unit tests (no database required)
│   └── postgresql_integration_test.go  # integration tests (//go:build integration)
├── prometheus/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/prometheus
│   ├── prometheus.go        # component lifecycle (Start, Stop, Health), metrics endpoint
│   ├── observer.go          # Observer — supervisor telemetry into a registry
│   └── prometheus_test.go   # unit tests (no scrape target required)
├── rabbitmq/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/rabbitmq
│   ├── rabbitmq.go          # component lifecycle (Start, Stop, Health)
│   ├── messaging.go         # publish/subscribe API
│   ├── rabbitmq_test.go              # unit tests (no broker required)
│   └── rabbitmq_integration_test.go  # integration tests (//go:build integration)
├── redis/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/redis
│   ├── redis.go             # component lifecycle (Start, Stop, Health)
│   ├── client.go            # Client interface, Set/Get/Del/Scan/… operations
│   ├── redis_test.go              # unit tests (no server required)
│   └── redis_integration_test.go  # integration tests (//go:build integration)
├── s3/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/s3
│   ├── s3.go                # component lifecycle (Start, Stop, Health)
│   ├── storage.go           # Storage interface (the seam adapters depend on)
│   ├── operations.go        # Upload/Download/Delete/ListKeys/Presign operations
│   ├── internal.go          # credential provider, HTTP error classification
│   ├── s3_test.go              # unit tests (no S3 endpoint required)
│   └── s3_integration_test.go  # integration tests (//go:build integration)
├── sqlite/
│   ├── go.mod               # module: github.com/sunkek/samsara-components/sqlite
│   ├── sqlite.go            # component lifecycle (Start, Stop, Health)
│   ├── config.go            # Config, DSN construction, pool sizing
│   ├── db.go                # DB interface, query API, transactions
│   ├── sqlite_test.go              # unit tests (temp-file and in-memory databases)
│   └── sqlite_integration_test.go  # integration tests (//go:build integration)
├── docs/adr/                # architecture decision records
├── CONTEXT.md               # glossary — the vocabulary all modules share
├── AGENTS.md                # working guide (agents and humans alike)
├── scripts/
│   ├── coverage-baseline.txt # per-module coverage floor, enforced by make coverage-check
│   ├── seaweedfs-init.sh    # creates the test bucket once SeaweedFS is healthy
│   └── seaweedfs-s3.json    # static credentials config for SeaweedFS integration tests
├── docker-compose.yml       # test infrastructure (Postgres, Redis, RabbitMQ, SeaweedFS)
└── Makefile
```

Each module is versioned independently. Changes to `postgresql` do not affect
`redis` consumers.

---

## Getting started

```bash
git clone https://github.com/sunkek/samsara-components
cd samsara-components

# Run unit tests (no Docker required)
make test-race

# Run all checks before opening a PR
make check
```

For integration tests, you need Docker with Compose v2:

```bash
make test-all       # starts infra, runs all tests, stops infra
```

---

## Before opening a pull request

Every PR must pass:

```bash
make check           # gofmt + go vet + staticcheck + unit tests with race detector
make test-all        # unit + integration
make coverage-check  # per-module coverage against the recorded baseline
make vuln            # govulncheck across all modules
```

The CI pipeline enforces all four. PRs that fail CI will not be merged.

### Coverage baseline

`scripts/coverage-baseline.txt` records each module's coverage in two columns:
unit (no Docker) and integration (`-tags integration`, services up).
`make coverage-check` reads the unit column and fails when a module falls more
than 2 points below it; CI runs it on every PR. If a change legitimately moves
the numbers — including upwards — run `make coverage-update` and commit the
file, or `make coverage-update-integration` behind `make infra-up` to refresh
both columns.

Both columns are recorded with the Go version CI pins (`COVERAGE_TOOLCHAIN` in
the Makefile, `go-version` in `.github/workflows/ci.yml`), because coverage
percentages shift between Go releases — enough to trip the gate on their own.
The coverage targets force that toolchain, so a local run matches CI even on a
different default Go.

The gap between the columns is by design: `fiber`, `grpc`, and `s3` keep most of
their behaviour behind a live server or endpoint, so a low unit number does not
mean thin testing. Treat a *drop* as the signal, not a low number.

### Ports

`docker-compose.yml` publishes each service on its standard port, overridable
per service so a machine that already runs one can move it:

```bash
SC_POSTGRES_PORT=55442 make test-integration
```

`SC_POSTGRES_PORT`, `SC_REDIS_PORT`, `SC_RABBITMQ_PORT`, and `SC_S3_PORT` are
read by both Compose and the integration tests, so both sides move together.

### Vulnerability scanning

`make vuln` runs `govulncheck` per module. It fails only on advisories whose
vulnerable symbols this code actually calls, so an unfixable advisory in a
required-but-uncalled module reports without blocking unrelated PRs.

### Quality expectations

- **Tests**: new behaviour must be covered by tests. Prefer unit tests
  (no database); add integration tests for anything that touches the wire.
- **Race detector**: all tests pass under `-race`. This is non-negotiable.
- **No new external dependencies** without prior discussion. The library aims
  to stay lean.
- **Docs**: exported types, functions, and methods must have Go doc comments.
- **Error wrapping**: use `fmt.Errorf("context: %w", err)` — never swallow
  or discard errors.

---

## Adding a new component

1. Create a new directory, e.g. `redis/`.
2. Initialise a module: `cd redis && go mod init github.com/sunkek/samsara-components/redis`.
3. Add the module to the workspace: `go work use ./redis` (from the repo root).
4. Implement `Name() string`, `Start(ctx, ready)`, `Stop(ctx)` — satisfying
   the samsara component contract. All public registration APIs (route
   registration, exchange declarations, subscriptions) must be called before
   `Start`; the component re-applies them on each restart from stored slices.
5. Implement `Health(ctx) error` if the component can be health-checked.
6. Add a compile-time assertion (see `postgresql/postgresql.go` for the pattern).
7. Add unit tests (no infrastructure required) and integration tests
   (`//go:build integration`).
8. Add a `README.md` in the component directory documenting Config, Options,
   and the public API.
9. Add the component's Docker Compose service to the root `docker-compose.yml`
   if integration tests need it.
10. Update the component table in the root `README.md`.

---

## Commit style

Use concise, imperative commit messages:

```
postgresql: add WithName option
ci: pin action SHAs
readme: document transaction pattern
```

---

## Reporting issues

Please include:
- Go version (`go version`)
- samsara and samsara-components versions
- A minimal reproducing example or test case