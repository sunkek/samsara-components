# Driver types appear in component interfaces

`postgresql` returns `pgconn.CommandTag` and `pgx.Tx`; `rabbitmq` hands handlers
an `amqp.Delivery`; `grpc` takes `grpclib.ServerOption`. These components do not
abstract their drivers behind neutral types.

The alternative — a driver-agnostic surface — was rejected: it would either
shrink to the intersection of what every driver supports (losing pgx's batching,
AMQP headers, gRPC interceptors) or grow into a second driver API to maintain.
Callers already chose PostgreSQL when they imported `postgresql`; hiding pgx
buys them nothing they asked for.

## Consequences

- A major driver release is a breaking change for the component's callers, not
  just for the component.
- Tests that need a seam use the narrow interface each component declares
  (`postgresql.DB`, `sqlite.DB`, `redis.KV`, `s3.Storage`,
  `rabbitmq.Publisher`) rather than faking the driver.
