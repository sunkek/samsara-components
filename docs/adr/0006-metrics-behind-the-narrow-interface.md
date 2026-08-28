# Metrics go behind the narrow interface, not on a new exported seam

[ROADMAP X1](../../ROADMAP.md) asks for per-operation metrics: request counts,
latencies, error rates, pool saturation. Consumers cannot see any of it today
without wrapping every call themselves.

The question is not whether to emit metrics but where the seam goes. We put the
instrumentation *behind* the interfaces callers already depend on —
`postgresql.DB`, `sqlite.DB`, `redis.KV`, `s3.Storage`, `rabbitmq.Publisher` —
via an unexported per-module helper. The exported surface does not grow.

## Considered options

- **Behind the existing interface** (chosen). Each operation routes through an
  unexported helper that times the call, classifies the error, and reports.
  `KV`, `DB`, `Storage` and `Publisher` keep exactly the methods they have. The
  helper also absorbs the not-ready check and the error tagging that every
  operation currently repeats by hand — nine near-identical bodies in
  `redis/client.go` alone.
- **A new exported `Collector` seam**, configured by an option. It admits any
  sink a caller writes, but it publishes an interface we would have to keep
  stable across nine modules that copy their boilerplate verbatim
  ([ADR-0002](./0002-duplicated-logger-and-option-boilerplate.md)) — one wrong
  shape gets copied nine times before anyone uses it. One production sink is a
  hypothetical seam, not a real one; two would change this.
- **Leave it to callers.** A decorator around `KV` or `DB` at each consumer.
  No new interface anywhere, and no shared implementation either: every
  consumer re-derives the same operation names, the same error classification,
  and the same latency buckets. This is the status quo X1 records as a gap.
- **A shared metrics module**, depended on by all nine. Unlike `Logger`, this
  is not forty lines of interface: timing, error classification, cardinality
  control and bucket definitions are real implementation, so
  [ADR-0002](./0002-duplicated-logger-and-option-boilerplate.md)'s argument
  does not carry over on its own. It fails on a fork instead. Carrying the
  Prometheus dependency makes every consumer of every component pull
  `client_golang` whether or not they enable metrics — unconditionally, which
  is worse than the driver coupling [ADR-0001](./0001-one-module-per-component.md)
  was written to prevent. Carrying no dependency — interfaces and a timing
  helper, with the Prometheus adapter left to a module the consumer imports —
  shrinks it back to forty lines of interface, where ADR-0002 applies
  unchanged. No version of it is both worth having and safe.

## Why this deepens the interfaces rather than widening them

`redis.KV` is nine methods over bodies that are a nil check, one go-redis call,
and an error wrap. `postgresql.DB` is four pgxscan pass-throughs and
`CommitTx`. Both are close to as complex at the interface as they are inside —
callers learn a lot of surface for comparatively little behaviour, and neither
hides its driver: `pgx.Tx` and `pgconn.CommandTag` are in `DB`'s signatures by
[ADR-0003](./0003-driver-types-at-the-interface.md).

Instrumentation is the behaviour that changes that balance. It is real work —
timing, error classification, cardinality control on keys and SQL — and it
belongs to every operation, so it lands in the one place all of them already
pass through. The interface stays the size it is and the implementation grows,
which is the trade the `uploadEngine` port already makes in `s3`
([ADR-0004](./0004-transfermanager-behind-an-internal-port.md)).

## Rules

- **The helper stays unexported**, one copy per module, like `Logger` and
  `Option`.
- **The sink is a tunable**, so it is configured through `Config` with an
  unexported accessor supplying the no-op default, not through `Option`.
- **The sink is a callback, not an interface.** `Config.OnOperation
  func(op string, d time.Duration, err error)`, defaulting to nil. A `Config`
  field needs a type, so "export nothing" was never available — the choice is
  only which shape to export. A single func field is the smaller commitment:
  callers pass a closure rather than implementing a method set we would have to
  keep stable across nine verbatim copies, and a `Collector` interface stays
  addable later, without a break, when a second production sink justifies it.
- **The sink sees this module's error vocabulary**, not the driver's. A miss
  and an unattempted call are not failures and a sink cannot classify what it
  cannot see, so the module's own sentinels reach it unchanged — `redis.ErrNil`
  for an absent key, `ErrNotReady` for no live connection. Each `Config` field
  documents its module's sentinels.
- **An unattempted operation reports a zero duration.** With no live connection
  there is no driver call to time, and reporting elapsed wall time there would
  put a meaningless sample in the latency distribution.
- **One observation per exported call**, not per round trip. `redis.Scan` runs
  a cursor loop and reports once; the caller made one call, and that is the
  latency they experience. Where the two differ the doc comment says so.
- **Zero value stays usable.** `Config{}` produces a component whose metrics
  are a no-op, and `TestConfig_ZeroValueNoPanic` continues to pass unchanged.
- **Operation names are ours**, fixed per method — `redis.set`, `postgres.get`
  — never derived from a key, a SQL string, a bucket, or a routing key. Those
  are unbounded and would blow up label cardinality.
- **Instrumentation may not change behaviour.** No new error, no altered error
  chain, no ordering change. A failing or slow sink is dropped, not surfaced.

## Consequences

- Operations reached through the driver accessors — `Pool()`, `Client()`,
  `Conn()`, `App()` — are not measured. That extends the bypass
  [ADR-0005](./0005-driver-escape-hatch-accessors.md) already accepts for
  logging, health and lifecycle, but it is worse here: unmeasured traffic makes
  the published numbers wrong rather than merely unlogged. Each narrow
  interface documents that its metrics cover calls through the interface only,
  and each accessor's doc comment says the same.
- The Prometheus client dependency must not reach the eight other modules. It
  lives behind a build tag or a subpackage per
  [ADR-0001](./0001-one-module-per-component.md); the default build gains no
  dependency.
- Nine copies of the helper to keep diffable, one per module, per ADR-0002.
  Prototyping in `redis` first — the shallowest interface with the most
  repetition to absorb — settles the shape before it is copied.
- The helper is analogous across modules, not verbatim, because the modules it
  wraps are not. `redis`, `sqlite` and `postgresql` fetch the one handle and
  carry the not-ready check, which is where the repetition they absorb lived;
  `s3` has three handles — client, presigner, upload engine — and leaves the
  check inside each operation; `rabbitmq` threads the operation name into the
  single publish path the three methods already shared. The verbatim rule in
  ADR-0002 covers `Logger`, `Option`, `WithLogger` and `WithName`, and does not
  extend here.
- `fiber`, `grpc`, `grpcclient` and `prometheus` are not instrumented: none has
  a per-operation caller surface. Serving components want request middleware,
  which is a different design; `prometheus` is the sink, not a source.
- Replication surfaced that the not-ready vocabulary was not stable enough for
  the sink rule above to hold, and closing that came with it: `redis`,
  `sqlite`, `postgresql`, `s3` and `rabbitmq` now each export `ErrNotReady`, so
  one `errors.Is` check reads the same across all five. `postgresql` previously
  panicked on an unstarted component and now returns the sentinel; `sqlite`'s
  was unexported; `s3` and `rabbitmq` returned unclassifiable message-only
  errors. Error text is unchanged in every case.
- Instrumenting a component is a minor release of that module: `Config` gains
  an exported field. Independent versioning
  ([ADR-0001](./0001-one-module-per-component.md)) keeps each one separate, so
  the nine need not move together.
- Pool saturation and connection-count gauges do not fit a per-call helper;
  they are sampled from the driver handle and are a separate follow-up.
- What is genuinely shared here is convention, not code: the operation-name
  scheme, the latency buckets, the error classification, the cardinality rule.
  Those are the parts that would drift across nine independent copies, and they
  are recorded above as prose, which costs no version edge.
- This holds while the helper stays close to its timing-and-classify core. If
  it grows exemplars, span propagation, or OpenTelemetry semantic-convention
  mapping, nine copies stop being maintainable and the shared-module question
  is worth reopening on those grounds — not on the ones rejected here.
