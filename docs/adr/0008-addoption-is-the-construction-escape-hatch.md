# `AddOption` is the construction-time escape hatch

[ADR-0005](./0005-driver-escape-hatch-accessors.md) gave every component an
accessor returning its live driver handle, which closes the gap for operations
the narrow interface does not wrap. It does not close the other half of the
gap: settings that must be supplied *while the handle is being built*.

A `pgxpool.Config` is read once, when `pgxpool.NewWithConfig` runs; a
`redis.Options` once, in `redis.NewClient`; an `s3.Options` once, in
`s3.NewFromConfig`. `Pool()`, `Client()` and `Client()` all hand back the
finished object, so connection lifetimes, `AfterConnect` hooks, retry strategy,
a custom HTTP client or dialler are unreachable through them. Before this ADR
each such setting was a change to this repository and a release: a `Config`
field, an accessor for it, and a line in `Start`.

`grpc` and `grpcclient` already had the answer. Both accumulate native driver
options through `AddOption` and apply them when the handle is built, because
interceptors cannot be attached any other way. `postgresql`, `redis` and `s3`
now take the same method, with the native type each driver builds from:

| module | mutator |
| --- | --- |
| postgresql | `AddOption(func(*pgxpool.Config))` |
| redis | `AddOption(func(*redis.Options))` |
| s3 | `AddOption(func(*s3.Options))` |
| grpc | `AddOption(grpc.ServerOption)` |
| grpcclient | `AddOption(grpc.DialOption)` |

The two shapes differ because the drivers do: grpc-go publishes option
functions, while pgx, go-redis and the AWS SDK publish a config struct the
caller mutates. Each module takes the shape its own driver uses, which is the
same rule ADR-0003 follows for types at the interface.

## Considered options

- **`AddOption` on the three that lacked it** (chosen). One method name across
  five modules, the pattern already proven in `grpc`, and the driver's own
  vocabulary in the signature.
- **A `Config` field per setting, on demand.** This is what the components did.
  It keeps the driver out of the exported surface, but ADR-0007 identifies
  `Config`'s field count as the real depth pressure, and the long tail of
  driver settings is exactly the growth that argument warns against.
- **Accept a fully built handle in `New`.** Maximum control, but the component
  no longer owns the handle it must dial on restart, which breaks the lifecycle
  the whole repository is built on.

## Rules

- **Mutators apply after the component's own settings.** A mutator can override
  anything `Config` set, including `postgresql.MaxConns` and the endpoint.
  That is intended: the escape hatch is worth nothing if the component wins
  every conflict. The ordering is documented on each method.
- **Mutators are kept, not consumed.** They are stored on the component and
  re-applied on every `Start`, so a supervisor restart rebuilds the handle with
  the caller's settings intact. A component that applied them once would
  silently lose them on the first restart — the failure mode being avoided
  here.
- **Call before `Start`.** A mutator added later affects the next `Start`, not
  the running handle; there is no reconfiguration path, and none is implied.
- **Guarded by their own mutex.** `AddOption` may be called from a different
  goroutine than `Start`, and the slice is copied under the lock before
  application, so a mutator added mid-`Start` cannot race the build.
- **Not a substitute for a `Config` field.** A setting most callers need is
  still a field with a default at the point of use (ADR-0007). `AddOption`
  carries the tail.

## Why `fiber` is excluded

`fiber` builds its app from a `fiber.Config` *value* passed to `fiber.New`,
and the useful additions to a Fiber app — middleware, routes — are already
served by `Use` and `Register`, which run at the right point in `Start`. A
`func(*fiber.Config)` mutator would cover only the struct's own fields, most of
which the component already models. The gap that motivated this ADR is not
present there, so `fiber` keeps `App()` (with its documented
mutate-before-`Start` constraint) and no `AddOption`. `prometheus`, `rabbitmq`
and `sqlite` are unaddressed for the same reason — no observed need — and this
ADR is the precedent for adding one if that changes.

## Consequences

- The driver's config type is now in an exported signature for three more
  modules, so a major driver release breaks callers who used it. This is the
  coupling [ADR-0003](./0003-driver-types-at-the-interface.md) already accepts,
  contained per module by independent versioning
  ([ADR-0001](./0001-one-module-per-component.md)).
- A caller can now build a handle the component's own tests never exercise —
  a mutator can disable retries, swap the HTTP client, or point the pool
  somewhere else. Health checks and `ErrNotReady` still behave, because they
  read the handle rather than assume its settings.
- Pressure on `Config` to grow should fall: the answer to "the component does
  not expose X" is a mutator for the tail, and a field only when X is common.
