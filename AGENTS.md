# Working in samsara-components

Nine independent Go modules — `fiber/`, `grpc/`, `grpcclient/`, `postgresql/`,
`prometheus/`, `rabbitmq/`, `redis/`, `s3/`, `sqlite/` — tied together for local
development by `go.work`. Each exports one `Component` implementing the samsara
lifecycle (`Start`, `Stop`, `Health`).

[CONTEXT.md](./CONTEXT.md) is the glossary. Read it before naming anything, and
when a term in the code reads ambiguously — component, ready signal, config,
option, subscription, retry topology.

## Before you edit

Read the module's own `<module>.go` first. The nine are deliberately
near-identical in shape, so the answer to "how should this look?" is almost
always "like the other eight": `Component` struct, `New(cfg, opts...)`,
lifecycle triple, `Config` with unexported accessors supplying defaults.

File names follow the concern, and vary by module: `Config` lives in
`config.go` where the module has one and in `<module>.go` otherwise; the
caller-facing surface is `client.go`, `db.go`, `messaging.go`, `operations.go`,
or `observer.go` depending on what the component does. Tests sit next to the
code, integration tests in `*_integration_test.go` behind
`//go:build integration`.

Read the ADR before reopening one of these — each records a trade-off already
settled, and the reasons are not visible in the code:

- [ADR-0001](./docs/adr/0001-one-module-per-component.md) — why nine modules and
  no shared internal package.
- [ADR-0002](./docs/adr/0002-duplicated-logger-and-option-boilerplate.md) — why
  `Logger` and `Option` are copied nine times.
- [ADR-0003](./docs/adr/0003-driver-types-at-the-interface.md) — why pgx, amqp,
  and grpc types appear in exported signatures.

## Conventions

- **Scope:** stay in the module you were asked about. Sharing a workspace is not
  a reason to touch the other eight.
- **Tunables belong in `Config`**, with an unexported accessor supplying the
  default. `Option` carries dependencies and identity (`WithLogger`,
  `WithName`); `fiber.WithSwagger` is the one tunable that arrived as an
  `Option`, and it stays the exception.
- **Zero value works:** `Config{}` produces a usable component, and every
  module has a `TestConfig_ZeroValueNoPanic` asserting it. A new module adds
  one.
- **Errors:** wrap with a tag and the operation —
  `fmt.Errorf("rabbitmq: declare exchange %q: %w", name, err)`. The tag is the
  module name, except `postgresql`, which tags `postgres:`.
- **Boilerplate stays identical:** the nine copies of `Logger`, `nopLogger`,
  `Option`, `WithLogger`, and `WithName` are copied verbatim, so the nine stay
  diffable. Change one and change all nine the same way.
- **Depend on the seam, not the component:** the caller-facing surface is
  declared as an interface — `postgresql.DB`, `sqlite.DB`, `redis.Client`,
  `s3.Storage`, `rabbitmq.Publisher` — with a `var _ Iface = (*Component)(nil)`
  assertion beside it. A new operation goes on both.
- **Doc comments:** every exported identifier. State the pre-`Start` behaviour
  when a method is callable before `Start`.
- **Import the runtime nowhere.** Components satisfy samsara's interfaces
  structurally, and that is what keeps the modules independent of it.

## Verifying

Run `make` targets rather than ad hoc `go` commands, so results match CI. The
`Makefile` lists them; `make check` before pushing, `make test-all` before
opening a PR. `make lint` installs `staticcheck` if it is missing.

Integration tests need the Docker services in `docker-compose.yml`;
`make test-integration` brings them up and down around the run.

One module at a time:

```bash
cd postgresql && go test -race -count=3 ./...
cd postgresql && go test -race -count=1 -tags integration ./...
```

Unit tests cover lifecycle and config; integration tests cover real network
behaviour. A behaviour change lands with a test at the level that can observe
it.

## Commits and PRs

Short, imperative, scoped to a module or a behaviour: `postgresql: add WithName
option`. PRs: summary, tests for behaviour changes, linked issue, and updates to
the module's `README.md` and the root `CHANGELOG.md` when a public API or config
field moves.
