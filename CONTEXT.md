# samsara-components

Infrastructure components for the [samsara](https://github.com/sunkek/samsara)
lifecycle runtime. This context is the vocabulary shared by all nine modules;
each module speaks it identically, which is why a reader who learns one module
can read the rest.

## Language

**Component**:
The single exported type of a module (`Component`), owning one piece of
infrastructure — a server, a pool, a connection — for the whole process
lifetime.
_Avoid_: service, client (a `Component` may expose a client; it is not one),
wrapper.

**Lifecycle**:
The `Start` / `Stop` / `Health` triple a supervisor calls on a component. A
running phase, not a construction step.
_Avoid_: init, bootstrap, run loop.

**Ready signal**:
The one-shot notification a component raises when it becomes usable, releasing
the supervisor's next tier.
_Avoid_: ready flag, ready channel, signal.

**Structural satisfaction**:
Components match samsara's interfaces by method set alone, never by importing
samsara. This is why no module depends on the runtime it is built for.
_Avoid_: implements, conforms to (both imply a declared dependency).

**Config**:
A component's tunables, valid at their zero value, with every default supplied
at the point of use rather than by a constructor or a validate step.
_Avoid_: options struct, settings (`Option` means something else here).

**Option**:
A component's dependencies and identity, supplied at construction —
`WithLogger`, `WithName`. Distinct from `Config`, which holds tunables.
_Avoid_: functional option, setting.

## RabbitMQ messaging

**Subscription**:
A queue binding paired with its handler, owned by the component and re-applied
on every start, so a restart restores the full topology.
_Avoid_: consumer (that is the AMQP-side object), listener.

**Retry topology**:
A subscription's retry queue, dead-letter queue, and the republish path between
them, all derived from its `RetryPolicy`.
_Avoid_: retry mechanism, backoff setup.

**Dead-letter queue (DLQ)**:
The terminal destination of a delivery that exhausted its retries. Nothing in
these components consumes from it.
_Avoid_: failure queue, error queue.

## Seams and escape hatches

**Narrow interface**:
The caller-facing surface of a component, declared as an interface next to the
component that satisfies it — `postgresql.DB`, `sqlite.DB`, `redis.KV`,
`s3.Storage`, `rabbitmq.Publisher`. This is the seam callers depend on, tests
exercise, and fakes implement.
_Avoid_: contract, port (that word is taken, below), abstraction.

**Escape hatch accessor**:
The exported accessor returning a component's driver handle — `Pool`, `Client`,
`App`, `Conn`, `Server`, `Channel`, `SQLDB`, `Registry` — for the long tail of
driver features the narrow interface does not wrap. Nil before `Start` and
after `Stop`, and never a member of the narrow interface. `prometheus.Registry`
is the one accessor live from `New`, because collectors are registered on it
before scraping begins ([ADR-0005](./docs/adr/0005-driver-escape-hatch-accessors.md)).
_Avoid_: raw accessor, unwrap, getter.

**Driver option**:
A native driver setting accumulated on a component before `Start` through
`AddOption`, applied when the handle is built and re-applied on every restart —
`func(*pgxpool.Config)`, `func(*redis.Options)`, `func(*s3.Options)`, and the
grpc pair's `DialOption`/`ServerOption`. The construction-time half of the
escape hatch, where the accessor is the runtime half
([ADR-0008](./docs/adr/0008-addoption-is-the-construction-escape-hatch.md)).
Distinct from `Option`, which is this repository's own construction vocabulary.
_Avoid_: raw option, driver config, override.

**Port**:
An *unexported* interface a component depends on internally, so an unstable
driver stays out of the exported surface. `s3`'s `uploadEngine` is the only
one.
_Avoid_: adapter (that is the thing satisfying the port), driver interface.

**Not ready**:
The state of a component with no live handle — before `Start`, after `Stop`,
mid-restart. Every operation on a narrow interface returns the module's
exported `ErrNotReady` in that state, so callers make one `errors.Is` check.
_Avoid_: closed, disconnected, uninitialised.

## HTTP (fiber)

**RegisterFunc**:
A caller-supplied callback receiving the root router, already scoped to
`Config.PathPrefix`, on which the caller registers routes and sub-group
middleware. Called in registration order during `Start`, after the built-in
middleware stack.
_Avoid_: handler, route registrar, mount func.

**Path prefix**:
The URL prefix every registered route sits under, applied once by the component
rather than repeated by each `RegisterFunc`.
_Avoid_: base path, mount point.

**Skipper**:
A predicate deciding, per request, that a middleware should not run. Built by
`ExcludeRoutes` from a set of `Route` values.
_Avoid_: filter, matcher, exclusion.

**HTTPStatuser**:
An error that carries its own HTTP status code, which the default error handler
honours instead of falling back to 500.
_Avoid_: status error, coded error.

## gRPC (grpc, grpcclient)

**RegisterFunc**:
The server-side equivalent of fiber's: a callback receiving the
`*grpc.Server` on which the caller registers service implementations, called
during `Start`.
_Avoid_: service registrar, binder.

**Dial option**:
A `grpc.DialOption` accumulated on `grpcclient` before `Start` via `AddOption`,
applied when the connection is established. The server-side equivalent is a
`grpc.ServerOption` on `grpc`. Both are driver options (below); the grpc pair
keep the driver's own names.
_Avoid_: client option, connection setting.

## Metrics (prometheus)

**Observer**:
The bridge from samsara supervisor telemetry into a Prometheus registry,
satisfying `samsara.MetricsObserver` structurally. Its methods run on the
supervisor goroutine and are therefore non-blocking in-memory updates only.
_Avoid_: collector, reporter, metrics sink.

**Operation callback**:
The per-operation hook a data component invokes — `Config.OnOperation`,
receiving operation name, duration and error — which is how metrics reach a
registry without a new exported seam. See
[ADR-0006](./docs/adr/0006-metrics-behind-the-narrow-interface.md).
_Avoid_: metrics hook, instrumentation callback.

## Object storage (s3)

**Upload engine**:
The unexported port through which the component writes object bodies, keeping
the pre-1.0 transfer manager out of the exported surface. See
[ADR-0004](./docs/adr/0004-transfermanager-behind-an-internal-port.md).
_Avoid_: uploader, transfer manager (that is the one adapter, not the port).

**Probe bucket**:
The bucket `HeadBucket` addresses in the connectivity check `Start` and
`Health` share. Empty `Config.HealthBucket` probes a synthetic name and treats
any answer as reachable; a configured real bucket makes the probe strict, so a
credential scoped elsewhere fails with `ErrProbeForbidden` rather than
reporting healthy.
_Avoid_: health bucket (that is the field), test bucket, canary.

**Canned ACL**:
An S3 access-control preset, carried as the `ACL` type and defaulting to
`ACLPrivate`.
_Avoid_: permission, access level.

**Presigned URL**:
A time-limited signed URL that lets a third party read or write one object
without credentials, built from a `PresignRequest`.
_Avoid_: signed link, temporary URL.

## SQL (postgresql, sqlite)

**TxFinaliser**:
The minimal transaction interface `CommitTx` requires — commit and rollback,
nothing else — so tests can supply a stub instead of a real database. The pgx
and `database/sql` versions differ in whether the methods take a context.
_Avoid_: transaction, tx handle.

**Commit-or-rollback**:
The `CommitTx(tx, inErr)` pattern: one call that commits when the incoming
error is nil and rolls back otherwise, so callers do not repeat the branch.
_Avoid_: finalise, close transaction.
