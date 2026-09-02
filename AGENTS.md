# Working in samsara-components

Nine independent Go modules — `fiber/`, `grpc/`, `grpcclient/`, `postgresql/`,
`prometheus/`, `rabbitmq/`, `redis/`, `s3/`, `sqlite/` — tied together for local
development by `go.work`. Each exports one `Component` implementing the samsara
lifecycle (`Start`, `Stop`, `Health`).

[CONTEXT.md](./CONTEXT.md) is the glossary. Read it before naming anything, and
when a term in the code reads ambiguously — component, ready signal, config,
option, narrow interface, port, subscription, retry topology.

## Before you edit

Read the module's own `<module>.go` first: the nine are near-identical, so
"like the other eight" is almost always the answer to "how should this look?"

When you are adding a file, hunting for one, or deciding where a test belongs,
read [docs/agents/module-layout.md](./docs/agents/module-layout.md) — file
names follow the concern and vary by module.

Three workflows have a skill in `.claude/skills/`, holding the checklist the
step needs: `new-module` (a tenth component), `seam-operation` (an operation on
a narrow interface), `boilerplate-sync` (a change to the five copied
identifiers).

Read the ADR before reopening one of these — each records a trade-off already
settled, and the reasons are not visible in the code:

- [ADR-0001](./docs/adr/0001-one-module-per-component.md) — why nine modules and
  no shared internal package.
- [ADR-0002](./docs/adr/0002-duplicated-logger-and-option-boilerplate.md) — why
  `Logger` and `Option` are copied nine times.
- [ADR-0003](./docs/adr/0003-driver-types-at-the-interface.md) — why pgx, amqp,
  and grpc types appear in exported signatures.
- [ADR-0004](./docs/adr/0004-transfermanager-behind-an-internal-port.md) — why
  the s3 upload engine sits behind an unexported port, unlike every other driver.
- [ADR-0005](./docs/adr/0005-driver-escape-hatch-accessors.md) — why every
  component exports an accessor for its driver handle, and what that accessor
  may not be attached to.
- [ADR-0006](./docs/adr/0006-metrics-behind-the-narrow-interface.md) — why
  metrics go behind `DB`/`KV`/`Storage`/`Publisher` instead of on a new
  exported collector seam.
- [ADR-0007](./docs/adr/0007-config-fields-are-the-interface.md) — why `Config`
  keeps one unexported accessor per tunable, and why field count is the number
  that matters.
- [ADR-0008](./docs/adr/0008-addoption-is-the-construction-escape-hatch.md) —
  why five components take raw driver options through `AddOption`, what the
  mutators may override, and why `fiber` has none.

## Conventions

- **Scope:** stay in the module you were asked about. Sharing a workspace is not
  a reason to touch the other eight.
- **Driver settings the component does not model go through `AddOption`**, not
  a new `Config` field, on the five components that have it — `postgresql`,
  `redis`, `s3`, `grpc`, `grpcclient`. Mutators run after the component's own
  settings and are re-applied on every `Start`
  ([ADR-0008](./docs/adr/0008-addoption-is-the-construction-escape-hatch.md)).
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
  diffable. Change one and change all nine the same way; `make
  boilerplate-check` compares them and fails on drift. `WithName`'s doc comment
  is the one part that is module-specific.
- **Not ready is an error, not a panic.** A component with no live handle —
  before `Start`, after `Stop`, mid-restart — returns its exported
  `ErrNotReady` from every operation on its narrow interface, so callers can
  make one `errors.Is` check and choose to fail open. `redis`, `sqlite`,
  `postgresql`, `s3` and `rabbitmq` each export one under that name.
- **Depend on the seam, not the component:** the caller-facing surface is
  declared as an interface — `postgresql.DB`, `sqlite.DB`, `redis.KV`,
  `s3.Storage`, `rabbitmq.Publisher` — with a `var _ Iface = (*Component)(nil)`
  assertion beside it. A new operation goes on both.
- **Test at the seam.** Where a module has a narrow interface, that interface is
  the agreed seam and tests exercise behaviour through it, typed as the
  interface rather than as `*Component`. Tests for the lifecycle triple and for
  `Config` defaults sit on `*Component`, which is where that behaviour lives.
  Reach for `_internal_test.go` only when the behaviour genuinely cannot be
  observed through an exported surface — and never assert on a driver handle
  obtained from the escape hatch accessor, which is the caller's escape hatch,
  not a test seam.
- **Doc comments:** every exported identifier. State the pre-`Start` behaviour
  when a method is callable before `Start`.
- **Import the runtime nowhere.** Components satisfy samsara's interfaces
  structurally, and that is what keeps the modules independent of it.

## Verifying

Run `make` targets rather than ad hoc `go` commands, so results match CI:
`make check` before pushing, `make test-all` before opening a PR. For the full
set — coverage baselines, integration services, port overrides, per-module
invocations — read [docs/agents/verifying.md](./docs/agents/verifying.md).

## Commits, issues and PRs

Short, imperative, scoped to a module or a behaviour: `postgresql: add WithName
option`. PRs: summary, tests for behaviour changes, linked issue, and updates to
the module's `README.md` and the root `CHANGELOG.md` when a public API or config
field moves.

Issues and PRs live on GitHub. To resolve an issue reference, find the spec
behind a branch, or file one, read
[docs/agents/issue-tracker.md](./docs/agents/issue-tracker.md).
