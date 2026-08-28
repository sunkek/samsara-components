# Module layout

Where things live inside a module, for when you are adding a file or looking
for one. The nine modules are deliberately near-identical in shape, so the
answer to "how should this look?" is almost always "like the other eight".

## Files by concern

File names follow the concern, and vary by module:

- **`<module>.go`** — always present. The `Component` struct, `New`, the
  lifecycle triple, `Logger`/`nopLogger`, `Option`/`WithLogger`/`WithName`, and
  the internal handle getter.
- **`config.go`** — `Config` and its accessors, where the module has enough of
  them to warrant a file (`grpc`, `grpcclient`, `sqlite`). Otherwise `Config`
  lives in `<module>.go`.
- **The caller-facing surface** — `client.go` (redis), `db.go` (postgresql,
  sqlite), `messaging.go` (rabbitmq), `operations.go` and `storage.go` (s3),
  `observer.go` (prometheus), `routes.go` (fiber). Named for what the component
  does, and this is where the narrow interface and its
  `var _ Iface = (*Component)(nil)` assertion sit.
- **`metrics.go`** — the operation-callback plumbing, in the five data
  components that have it.
- **`tls.go`** — TLS config construction, where the driver needs it (`redis`,
  `grpc`, `grpcclient`).
- **`internal.go`, `upload.go`, `transfermanager_engine.go`** — s3 only, the
  unexported upload port and its adapter (see
  [ADR-0004](../adr/0004-transfermanager-behind-an-internal-port.md)).

## Tests

Tests sit next to the code they cover:

- **`<file>_test.go`** — the package's external test surface.
- **`<file>_internal_test.go`** — tests that need unexported identifiers
  (`fiber/routes_internal_test.go`, `s3/upload_internal_test.go`). Reach for
  this only when the behaviour genuinely cannot be observed through the
  exported surface.
- **`<module>_integration_test.go`** — behind `//go:build integration`, needing
  the Docker services in `docker-compose.yml`.

Unit tests cover lifecycle and config; integration tests cover real network
behaviour. A behaviour change lands with a test at the level that can observe
it.
