# Every component exports an accessor for its driver handle

Each component surfaces the operations its callers were observed to need, and
nothing else. Everything past that edge is unreachable: `CopyFrom`,
`LISTEN`/`NOTIFY` and batched queries in `postgresql`; pipelines, Lua `EVAL`,
hashes and streams in `redis`; `CopyObject`, `HeadObject`, range-GET and
versioning in `s3`; any Fiber feature `Register`/`Use` do not cover. Four
separate ROADMAP items (P1, R1, S2, F1) are the same request.

Every component therefore exports an accessor returning its driver handle —
`Pool() *pgxpool.Pool`, `Client() *redis.Client`, `Client() *s3.Client`,
`App() *fiber.App`, and so on. `grpcclient.Conn() *grpc.ClientConn` already
does this and is the precedent, not an exception.

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
  `Client`, `App`, `Conn`. Not `Raw` or `Unwrap`.
- **Nil before `Start` and after `Stop`.** The accessor takes the same lock as
  the internal getter and returns whatever is there. Document it, as
  `grpcclient.Conn` does, and point callers at `samsara.WithDependencies` when
  they need the handle at startup.
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
  after `Start` do not take effect. Its doc comment must say so.
