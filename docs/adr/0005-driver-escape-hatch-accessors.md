# Every component exports an accessor for its driver handle

Each component surfaces the operations its callers were observed to need, and
nothing else. Everything past that edge is unreachable: `CopyFrom`,
`LISTEN`/`NOTIFY` and batched queries in `postgresql`; pipelines, Lua `EVAL`,
hashes and streams in `redis`; `CopyObject`, `HeadObject`, range-GET and
versioning in `s3`; any Fiber feature `Register`/`Use` do not cover. Four
separate ROADMAP items (P1, R1, S2, F1) are the same request.

Every component therefore exports an accessor returning its driver handle:

| module | accessor |
| --- | --- |
| postgresql | `Pool() *pgxpool.Pool` |
| redis | `Client() *redis.Client` |
| s3 | `Client() *s3.Client` |
| sqlite | `SQLDB() *sql.DB` |
| rabbitmq | `Conn() *amqp.Connection`, `Channel() *amqp.Channel` |
| fiber | `App() *fiber.App` |
| grpc | `Server() *grpc.Server` |
| grpcclient | `Conn() *grpc.ClientConn` |
| prometheus | `Registry() *prometheus.Registry` (live from `New`; see the rules) |

`grpcclient.Conn()` already did this and is the precedent, not an exception.

## Considered options

- **Accessor on every component** (chosen). One rule to learn, and it closes
  P1, R1's remainder, S2 and F1 at once.
- **Widen each operation surface on demand.** Keeps the driver out of the
  exported API, but every unanticipated need becomes a release, and the
  components grow into a second driver API to maintain — precisely what
  [ADR-0003](./0003-driver-types-at-the-interface.md) rejected.
- **Accessor only where the lifecycle makes it safe** — everywhere but `fiber`,
  whose `*fiber.App` must be mutated before `Start`. Rejected: it trades one
  documented ordering constraint for a per-module rule callers have to memorise.

## Why this follows from ADR-0003

[ADR-0003](./0003-driver-types-at-the-interface.md) already puts driver types in
exported signatures, on the grounds that a caller who imported `postgresql`
chose pgx and gains nothing from having it hidden. The accessor is that same
argument applied to the operations the component does not wrap. It adds no new
kind of coupling; it makes the existing coupling useful.

It does not follow for a *pre-1.0* driver. That is the distinction
[ADR-0004](./0004-transfermanager-behind-an-internal-port.md) draws:
`feature/s3/transfermanager` stays behind an unexported port precisely because
exporting it would publish its churn. `s3.Client()` returns the GA
`*s3.Client`, not the transfer manager.

## Rules

- **Name it after the handle**, matching the driver's own vocabulary: `Pool`,
  `Client`, `App`, `Conn`, `Server`, `Channel`, `Registry`. Not `Raw` or
  `Unwrap`. Where that name is already the module's narrow interface, qualify it
  with the driver package rather than renaming the interface: `sqlite.SQLDB()`
  returns the `*sql.DB` because `sqlite.DB` is the seam.
- **One accessor per handle the caller can act on.** `rabbitmq` exports two —
  `Conn() *amqp.Connection` and `Channel() *amqp.Channel` — because an
  `amqp.Channel` is not safe for concurrent use, so the useful escape hatch is
  the connection, from which a caller opens a private channel. The shared
  channel is exposed for one-off synchronous calls, with that caveat documented.
- **Nil before `Start` and after `Stop`.** The accessor takes the same lock as
  the internal getter and returns whatever is there. Document it, as
  `grpcclient.Conn` does, and point callers at `samsara.WithDependencies` when
  they need the handle at startup.

  `prometheus.Registry()` is the one exception, and deliberately so: the
  registry is built in `New` rather than dialled in `Start`, and collectors
  must be registered on it *before* scraping begins, so a nil-until-`Start`
  accessor would make the component's main use impossible. It is live for the
  component's whole lifetime and needs no lock. A handle a component *owns*
  from construction may follow this shape; a handle it *acquires* in `Start` —
  a connection, a pool, a listening server — may not.
- **Never on the narrow interface.** `postgresql.DB`, `sqlite.DB`,
  `redis.KV`, `s3.Storage` and `rabbitmq.Publisher` are the seams adapters
  depend on and fake; putting an un-fakeable driver handle on them would make
  every one of them un-implementable outside this repository.
- **An accessor is not a substitute for an operation with real depth.** If a
  use is common, wrap it: the accessor exists for the long tail, not as a
  reason to stop designing the surface.

## Consequences

- A major driver release breaks callers who used the accessor, on top of the
  breakage ADR-0003 already accepts. Independent module versioning
  ([ADR-0001](./0001-one-module-per-component.md)) keeps that contained to the
  affected component.
- Callers can bypass component behaviour — logging, health, lifecycle — by
  operating on the handle directly. That is the trade being made; the narrow
  interfaces remain the supported path, and the accessor is documented as the
  escape hatch.
- `fiber.App()` carries an ordering constraint the others do not: mutations
  after `Start` do not take effect. Its doc comment must say so. `grpc.Server()`
  carries the same constraint for the same reason, and worse: grpc-go panics on
  `RegisterService` once `Serve` has begun, so `Register` and `AddOption` are
  the only supported way to add behaviour there.
- Honouring "nil after `Stop`" required a change in `grpc`, which previously
  left `c.server` set after shutdown. It is cleared now, in `Stop` and in
  `Start`'s context-cancellation path, so the accessor reports not-ready the
  same way the other eight do.
