# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go workspace monorepo. Each top-level component directory is an independent module with its own `go.mod`: `fiber/`, `grpc/`, `grpcclient/`, `postgresql/`, `rabbitmq/`, `redis/`, and `s3/`. Root-level `go.work` ties them together for local development. Keep module-specific docs in each module's `README.md`. Shared test infrastructure lives in `docker-compose.yml` and `scripts/`.

## Build, Test, and Development Commands

- `make test`: run unit tests across all modules.
- `make test-race`: run unit tests with the race detector; use this before pushing.
- `make check`: run `go vet`, `staticcheck`, and race-tested unit suites.
- `make test-integration`: start Docker Compose services, run `-tags integration` tests, then tear infra down.
- `make test-all`: run unit and integration tests together.
- `make coverage`: print per-module coverage summaries.
- `make tidy`: run `go mod tidy` in every module.

## Coding Style & Naming Conventions

Use standard Go formatting and layout: tabs for indentation, `gofmt` formatting, and idiomatic package naming. Keep public APIs small and explicit; exported identifiers require Go doc comments. Prefer descriptive file names by concern, such as `config.go`, `client.go`, or `operations.go`. Wrap errors with context using `fmt.Errorf("...: %w", err)`.

## Testing Guidelines

Place unit tests next to implementation as `*_test.go`. Integration tests use `*_integration_test.go` and the `integration` build tag. Favor unit coverage for component lifecycle and configuration logic, then add integration coverage for real network or container-backed behavior. Run `make check` for fast validation and `make test-all` before opening a PR.

## Commit & Pull Request Guidelines

Recent history favors short, imperative commit messages, for example `Polish config and shutdown` or `Nullify pool on stop`. Keep commits focused by module or behavior. Pull requests should include a clear summary, tests for behavior changes, linked issues when applicable, and updated README/docs when public APIs or configuration change.

## Agent-Specific Notes

Do not edit unrelated modules just because they share the workspace. Prefer the root `Makefile` over ad hoc commands so checks stay consistent with CI.
