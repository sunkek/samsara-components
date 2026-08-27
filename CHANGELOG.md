# Changelog

All notable changes to samsara-components are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Each component module is versioned independently; this file tracks changes
across all of them.

---

## Repository — 2026-08-27

Tooling and documentation shared by every module. Nothing here carries a
version: only the component modules are tagged.

### Added
- `CONTEXT.md` (shared vocabulary) and `docs/adr/` (five decision records).
- `make fmt-check` and `make fmt`, with `fmt-check` wired into `make check` and
  CI — formatting drift previously reached `main` unnoticed (#6).
- `make vuln` (`govulncheck` per module) and a CI job running it — nine
  dependency trees had nothing checking pinned versions against published
  advisories (#7).
- `scripts/coverage-baseline.txt` with `make coverage-check`,
  `make coverage-update`, and `make coverage-update-integration`, wired into CI:
  per-module coverage now has a recorded floor in both the unit and integration
  columns, so a drop is visible instead of silent (#8).
- `SC_POSTGRES_PORT`, `SC_REDIS_PORT`, `SC_RABBITMQ_PORT`, and `SC_S3_PORT` —
  host ports for the integration services, read by both `docker-compose.yml` and
  the integration tests, so a machine already running one of them can move it
  without editing tracked files.

### Fixed
- `CONTRIBUTING.md`'s repository tree omitted `prometheus/`, `sqlite/`, and
  `s3/storage.go`; `AGENTS.md`'s module list omitted `prometheus` and `sqlite`.
- ROADMAP items cited line numbers that no longer matched, and four of them
  described gaps that have since been closed.
- CI built on `go.work`'s `go 1.25.0`, which is the language minimum rather
  than a toolchain to build with. That shipped an unpatched standard library —
  `govulncheck` failed on 21 stdlib advisories fixed in go1.25.13 — and
  `staticcheck@latest` (v0.8.1) refused to install at all, since it requires
  go1.26. CI now builds on the latest stable Go, and both tool versions are
  pinned in the Makefile so a local run checks exactly what CI checks.

---

## s3/v0.3.0 — 2026-08-27

### Added
- `Client() *s3.Client` — the escape hatch for SDK operations `Storage` does not
  wrap: `CopyObject`, `HeadObject`, range GETs, versioning, bucket
  administration. Returns nil outside the started lifecycle
  ([ADR-0005](docs/adr/0005-driver-escape-hatch-accessors.md)).
- `Config.UploadPartSize` (default 8 MiB, floor 5 MiB) and
  `Config.UploadConcurrency` (default 5) — the memory/throughput trade-off for
  uploads is now tunable.
- `TestConfig_ZeroValueNoPanic`, plus the first unit tests of upload policy —
  content-type sniffing and ACL defaulting were previously reachable only
  through Docker.

### Fixed
- **`Upload` buffered the entire object in memory.** It read the whole body with
  `io.ReadAll` to obtain a seekable stream, so peak memory scaled with object
  size and large uploads could exhaust the process (#4). Uploads now stream
  through the AWS SDK's transfer manager as a multipart upload, bounded by
  `(UploadConcurrency+1) x UploadPartSize` regardless of object size. The body
  no longer has to be seekable or of known length, and content-type sniffing
  buffers only the leading 512 bytes. `Upload`'s signature is unchanged.

### Dependencies
- Adds `github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager` v0.3.17, which
  is **pre-1.0** and breaking before v1.0 is likely. It is reached only through
  an unexported port, so its types stay out of this module's exported surface
  ([ADR-0004](docs/adr/0004-transfermanager-behind-an-internal-port.md)).

### Verification note
- The streaming path is measured against SeaweedFS over plain HTTP, the
  integration environment this repository ships. It has not been exercised
  against real AWS S3.

---

## redis/v0.6.0 — 2026-08-27

### Changed
- **BREAKING:** the narrow interface is now `KV`, not `Client`. It was the only
  module naming its seam after the driver rather than the role it plays —
  `postgresql.DB`, `s3.Storage`, `rabbitmq.Publisher` — and it collided with the
  new `Client()` accessor. Adapters that declare a `redis.Client` field change
  the type name; nothing else moves, and code depending on `*Component` is
  unaffected.

### Added
- `Client() *redis.Client` — the escape hatch for go-redis features `KV` does
  not wrap: pipelines, Lua `EVAL`, hashes, sets, streams, pub/sub. Returns nil
  outside the started lifecycle
  ([ADR-0005](docs/adr/0005-driver-escape-hatch-accessors.md)).
- `TestConfig_ZeroValueNoPanic` — the zero-value `Config` contract was only
  asserted in `fiber`.

### Fixed
- The compile-time assertion that `*Component` satisfies the narrow interface
  was missing.

---

## postgresql/v0.3.0 — 2026-08-27

### Added
- `Pool() *pgxpool.Pool` — the escape hatch for pgx features `DB` does not wrap:
  `CopyFrom`, `LISTEN`/`NOTIFY`, batched queries, custom row handling. Returns
  nil outside the started lifecycle
  ([ADR-0005](docs/adr/0005-driver-escape-hatch-accessors.md)).
- `TestConfig_ZeroValueNoPanic` — the zero-value `Config` contract was only
  asserted in `fiber`.

### Fixed
- The compile-time assertion that `*Component` satisfies `DB` was missing.

---

## fiber/v0.6.0 — 2026-08-27

### Added
- `App() *fiber.App` — access to the underlying Fiber app
  ([ADR-0005](docs/adr/0005-driver-escape-hatch-accessors.md)). It is read-only
  in practice: the app is built inside `Start`, so `App` returns nil until Fiber
  is already listening, and routes registered afterwards are not served. Use
  `Register` and `Use` to add behaviour.
- `TestConfig_ZeroValueNoPanic`.

---

## rabbitmq/v0.4.0 — 2026-08-27

### Added
- **`Publisher`** — the interface domain adapters should depend on, matching
  `postgresql.DB`, `sqlite.DB`, `redis.KV`, and `s3.Storage`. Publish-only by
  design: subscription setup is wiring, not a domain concern.
- `TestConfig_ZeroValueNoPanic`.

---

## grpc/v0.2.1, grpcclient/v0.2.1, prometheus/v0.1.1, sqlite/v0.1.1 — 2026-08-27

### Added
- `TestConfig_ZeroValueNoPanic` in each — the zero-value `Config` contract was
  only asserted in `fiber`.

### Dependencies
- Routine updates picked up since the previous tag. No source changes.

---

## fiber/v0.5.0 — 2026-07-28

### Fixed
- **Security: a path-scoped `Use` call silently unscoped every other global
  middleware.** `Use` appended each call's arguments into one flat slice, and
  Start replayed them as a single `app.Use(...)`. Fiber reads a string argument
  there as the path prefix for **all** handlers in that call, so one
  `Use("/docs/file.json", static)` re-scoped auth, audit and tracing middleware to
  that single path — every other route was served unguarded and unaudited, with no
  error anywhere. `Use` now stores one group per call and Start replays each
  group as its own `app.Use`, so a path argument cannot leak between calls.

  **Impact:** any consumer that registered a path-scoped `Use` alongside global
  middleware. Auth and audit middleware registered via `Use` did not run for any
  route outside that path. Upgrade and verify that an unauthenticated request to a
  protected route is rejected.

### Changed
- **Internal only:** the stored middleware field is now `[][]any` (one entry per
  `Use` call). The public `Use(args ...any)` signature is unchanged; an empty
  `Use()` is now a no-op rather than a zero-argument group.

## redis/v0.5.0 — 2026-07-28

### Added
- `Incr(ctx, key) (int64, error)` — atomic `INCR`. A missing key counts as 0, so
  the first call returns 1, which is the signal callers use to arm a window TTL
  exactly once (`Incr`, then `Expire` when the result is 1). This makes a
  fixed-window counter — rate limits, quotas — expressible in one round trip and
  correct across replicas; `SetNX` alone could only claim, not count.

### Changed
- **Breaking for custom `Client` implementations:** the new method is part of
  the `Client` interface, so hand-written fakes and mocks must add it.
  `*Component` satisfies it; consumers depending on `Client` need no change.

---

## sqlite/v0.1.0 — 2026-07-20

### Added
- New module: embedded SQLite component backed by the pure-Go
  `modernc.org/sqlite` driver, so consumers keep building with
  `CGO_ENABLED=0`. Standard samsara lifecycle (`Start`/`Stop`/`Health`) plus
  a `DB` interface (`Select`/`Get`/`Exec`/`BeginTx`/`CommitTx`) whose
  signatures mirror the `postgresql` module, so adapters can move between the
  two. Scanning via `scany/v2/sqlscan`.
- `Start` verifies rather than assumes: it creates parent directories, pings,
  and confirms the requested `journal_mode` actually engaged — SQLite silently
  falls back to a rollback journal when WAL is unavailable (notably on network
  filesystems), which the component now treats as a startup failure instead of
  letting it surface later as unexplained `SQLITE_BUSY`.
- `Health` runs `SELECT 1` rather than a ping, since `database/sql` can satisfy
  a ping from a pooled connection whose underlying file has been deleted.
- Defaults chosen for correctness over throughput: `MaxOpenConns` is 1 (writes
  queue in the pool instead of racing for the SQLite write lock),
  `foreign_keys` is ON (bare SQLite defaults it off), `journal_mode` is WAL,
  `synchronous` is NORMAL. Pragmas are applied via DSN `_pragma` parameters so
  they cover every pooled connection, not just the first.

> **Release note (all modules below, 2026-07-20):** every component's local
> `Logger` interface is now the identical four-method set
> `Debug`/`Info`/`Warn`/`Error(msg string, args ...any)`. **Breaking:**
> consumer logger adapters missing `Debug` or `Warn` must add them. Non-fatal
> conditions (close/stop timeouts, TLS verification disabled) are now logged
> at `Warn` instead of `Error`/`Info`.

## prometheus/v0.1.0 — 2026-07-20

### Added
- New module: Prometheus metrics component. `Component` serves the
  exposition format over HTTP (default `:2112/metrics`) with the standard
  samsara lifecycle (`Start`/`Stop`/`Health`), and `Observer()` returns a
  `samsara.MetricsObserver`-compatible bridge that exports supervisor
  telemetry (component up/down, restarts, health-check latency) into the
  same registry. Application metrics register via `Registry()`. Go runtime
  and process collectors are on by default
  (`Config.DisableRuntimeCollectors` to opt out).

## rabbitmq/v0.3.0 — 2026-07-20

### Added
- `SubscribeOptions.Retry` (`*RetryPolicy`) — component-managed retry
  pipeline: delayed retries via a TTL delay queue (`<queue>.retry`) with
  configurable `MaxRetries`, `Backoff`, `BackoffMultiplier`, `MaxBackoff`,
  and a terminal dead-letter queue (`<queue>.dlq`) once retries are
  exhausted or the handler returns `ErrDropToDLX`. Queue names overridable
  via `RetryQueue`/`DLQ`. The attempt counter travels in the
  `x-retry-count` header (`RetryHeader`).
- `Health()` now reports unhealthy when a consumer goroutine has died
  while the component is running (broker-cancelled consumer or closed
  delivery channel), not just when the connection/channel is closed.

### Fixed
- `Start` no longer swallows subscription bind failures: a queue that fails
  to declare/bind/consume now fails `Start` (so the supervisor restart
  policy applies) instead of leaving the component running without that
  consumer. Consumer goroutines that exit unexpectedly are logged and
  surfaced through `Health()`.

## fiber/v0.4.0 — 2026-07-20

### Added
- `Config.ShutdownTimeout` — bounds the graceful shutdown triggered by
  samsara context cancellation (previously hardcoded 10 s, still the default).
- `Config.HealthTimeout` — bounds the `/health` probe HTTP client
  (previously hardcoded 5 s, still the default).

### Changed
- Lifecycle converged on the stopCh pattern used by the other components;
  the ctx-watcher goroutine no longer lingers across restarts.

## grpc/v0.2.0 — 2026-07-20

### Added
- TLS support: `Config.TLS`, `TLSCertFile`, `TLSKeyFile`, `TLSClientCAFile`
  (mTLS), `TLSMinVersion`. Plaintext remains the default.
- `Config.StopTimeout` — bounds the ctx-cancel graceful stop before the
  server is force-stopped (previously hardcoded 10 s, still the default).

## grpcclient/v0.2.0 — 2026-07-20

### Added
- TLS support: `Config.TLS`, `TLSServerName`, `TLSCAFile`, `TLSCertFile`/
  `TLSKeyFile` (mTLS), `TLSMinVersion`, `TLSInsecureSkipVerify`. Insecure
  plaintext remains the default when `TLS` is unset.

### Changed
- **Breaking:** default component `Name()` changed from `"grpc-client"` to
  `"grpcclient"` to match the module name. Update
  `samsara.WithDependencies("grpc-client")` references, or pin the old name
  with `WithName("grpc-client")`.

## postgresql/v0.2.0 — 2026-07-20

### Changed
- **Breaking:** default component `Name()` changed from `"postgres"` to
  `"postgresql"` to match the module name. Update
  `samsara.WithDependencies("postgres")` references, or pin the old name
  with `WithName("postgres")`.

## redis/v0.4.0 — 2026-07-20

### Changed
- Unified `Logger` interface (see release note above).

## s3/v0.2.0 — 2026-07-20

### Added
- `Storage` interface — the consumer-facing API (`Upload`, `Download`,
  `Delete`, `DeleteByPrefix`, `ListKeys`, `PresignDownload`, `PresignUpload`).
  `*Component` satisfies it; adapters should depend on `Storage`.

---

## rabbitmq/v0.2.0 — 2026-06-21

### Added

**rabbitmq**
- Native dead-letter / redelivery support, additive (no API removal):
  - `SubscribeWithOptions(exchange, queue, routingKey, handler, opts)` with
    `SubscribeOptions{QueueArgs amqp.Table, QueueType string}`. Declares the
    work queue with custom `x-arguments` (`x-dead-letter-exchange`,
    `x-message-ttl`, `x-delivery-limit`) so the broker owns dead-lettering and
    the redelivery cap at declare time. `QueueType` sets `x-queue-type`;
    `QueueTypeQuorum` const provided (quorum queues are required for
    `x-delivery-limit`). `Subscribe`/`SubscribeWithKey` are unchanged and now
    route through it with nil queue args.
  - `ErrDropToDLX` sentinel error. A handler that returns (or wraps with `%w`)
    it is nacked with `requeue=false`, firing a queue-level dead-letter policy.
    Any other error keeps the previous behaviour (nack with `requeue=true`).
  - `PublishWithHeaders(ctx, exchange, routingKey, contentType, headers, body)`
    stamps custom AMQP headers (e.g. an attempt counter) on a message.

---

## redis/v0.2.1 — 2026-06-17

### Fixed

**redis**
- `Set`, `Get`, `Del`, `Exists`, `Expire`, `TTL`, and `Scan` no longer panic
  with a nil-pointer dereference when the component has no live connection
  (before `Start`, after `Stop`, or while the supervisor restarts it because
  Redis is down). They now return the new sentinel `ErrNotReady` instead, so
  callers can fail open. Detect it with `errors.Is(err, redis.ErrNotReady)`.

### Added

**redis**
- `ErrNotReady` sentinel error, returned by all `Client` operations and
  `Health` when no connection is established.

---

## fiber/v0.3.0 — 2026-06-11

### Added

**fiber**
- `Config.ReadBufferSize` sets the per-connection buffer for reading the
  request line and headers, threaded into `gf.Config`. Raise it above the
  default (fasthttp's 4096) when clients send large headers (e.g. big
  Cookie). Zero preserves the fasthttp default.

---

## fiber/v0.2.0 — 2026-06-03

### Added

**fiber**
- Proxy-aware client info. `Config` gains `TrustProxy`, `TrustProxyConfig`,
  `ProxyHeader`, and `EnableIPValidation`, threaded into `gf.Config`. When
  `TrustProxy` is true and the request's immediate peer matches
  `TrustProxyConfig`, `c.IP()` reads `ProxyHeader` instead of the socket remote
  address. Defaults (`false`) preserve direct-exposure behaviour.
- CAUTION: fiber returns the LEFT-MOST `ProxyHeader` entry — spoof-safe only
  when a single proxy overwrites it. For append-style chains, resolve the
  client IP in caller middleware instead.

---

## redis/v0.2.0 — 2026-05-22

### Added

**redis**
- TLS support. `Config` gains `TLS`, `TLSCAFile`, `TLSCertFile`, `TLSKeyFile`,
  `TLSServerName`, `TLSInsecureSkipVerify`, and `TLSMinVersion` fields. When
  `TLS` is true, `Start` builds a `*tls.Config` and wires it into
  `redis.Options.TLSConfig`. `TLSServerName` defaults to `Host`;
  `TLSMinVersion` accepts `"1.2"` (default) or `"1.3"`. Optional client cert
  fields enable mutual TLS — both must be set together. Misconfiguration
  (unreadable CA, non-PEM CA contents, half-set cert/key, unknown
  `TLSMinVersion`) fails `Start` loudly; there is no plaintext fallback. A
  warning is logged on `Start` when `TLSInsecureSkipVerify` is true.
- `TestIntegration_TLS_Start` integration test gated on `REDIS_TLS_ADDR`
  (skipped otherwise). Reads optional `REDIS_TLS_{CA_FILE,SERVER_NAME,USER,PASS,INSECURE}`.

---

## s3/v0.1.5 — 2026-05-20

### Fixed

**s3**
- `Stop`: `c.client` and `c.presigner` are now cleared to nil under the write
  lock. Post-stop calls to `Upload`, `Download`, `Delete`, `DeleteByPrefix`,
  `ListKeys`, `PresignDownload`, or `PresignUpload` now return "client not
  initialised" instead of using a stale SDK client. Each operation also
  snapshots `getClient`/`getPresigner` into a local so the nil check and the
  call cannot race against `Stop` nulling the field between them.

---

## s3/v0.1.4 — 2026-04-27

### Added

**s3**
- `PresignRequest` now exposes `ContentType` and `ContentLength`, letting `PresignUpload` sign exact headers for constrained PUT uploads; the README and new integration test document the requirement that client requests send the same `Content-Type`/`Content-Length` values.

---

## grpc/v0.1.1, grpcclient/v0.1.1, fiber/v0.1.2, rabbitmq/v0.1.3, redis/v0.1.1, postgresql/v0.1.2, s3/v0.1.4 — 2026-05-20

### Fixed

**grpc**
- `keepaliveOptions`: all `ServerParameters` fields are now merged into a
  single `KeepaliveParams` call. Two separate calls caused the second struct's
  zero-valued fields to silently overwrite non-zero values from the first —
  e.g. setting `MaxConnectionAge` would zero out `Time`, `Timeout`, and
  `MaxConnectionIdle`.
- `Start`: eliminated double-`GracefulStop` race between the ctx-cancel
  goroutine and `Stop`. The goroutine now replicates `Stop`'s full state
  transition and issues `GracefulStop` with a 10 s force-stop fallback. Only
  one path ever calls `GracefulStop`.
- `Start`: goroutine launch moved before `ready()` so ctx cannot fire in the
  gap between `ready()` returning and the goroutine starting.

**fiber**
- `Stop`: `c.app` is now cleared to nil after `Listen` returns, making
  repeated `Stop` calls idempotent. Previously a second call would invoke
  `ShutdownWithContext` on an already-shut-down app and return an error.
- `Register`: removed the post-`Start` hot-registration path. Calling
  `app.Group` and `fn(root)` concurrently with a live `Listen` is not
  guaranteed thread-safe by Fiber. `Register` now only appends to the slice
  and must be called before `Run`.
- `Health`: replaced `http.DefaultClient` (no timeout) with a package-level
  `healthClient` with a 5 s timeout, preventing indefinite blocking when
  called with `context.Background` and the server is wedged.
- `Config`: added `CompressNext func(gf.Ctx) bool` and `DisableCompress bool`
  to opt streamed handlers out of the built-in compress middleware. Fiber's
  compress middleware calls `c.Response().Body()` in `shouldSkip`, which
  drains a streaming body (`SendStreamWriter`, SSE, chunked proxying) into a
  buffer before any byte hits the socket — clients otherwise receive the
  full response only after the upstream stream ends. Threaded through to
  `compress.Config{Next}`, which runs before `c.Next()` and avoids the drain.

**rabbitmq**
- Consumer goroutine leak on stop: `SubscribeWithKey`'s live-bind path
  previously passed `context.Background()` to `bindAndConsume`, so consumer
  goroutines started via post-`Start` `Subscribe` calls never exited on stop
  or restart. `runCtx`/`runCancel` fields (guarded by `mu`) now store the
  lifecycle context of the current `Start` call. `Stop` calls `runCancel`
  before closing the connection; `Start` calls it in its terminal block.
  `SubscribeWithKey` reads `runCtx` under `RLock` and passes it to
  `bindAndConsume`.
- `Stop`: `c.conn` and `c.ch` are now cleared to nil under the write lock so
  post-stop accessors see an uninitialised state instead of a stale handle to
  a closed connection.

**redis**
- `Stop`: `c.client` is now cleared to nil under the write lock so post-stop
  accessors see an uninitialised state instead of a stale handle to a closed
  pool.

**postgresql**
- `Stop`: `c.pool` is now set to nil under the write lock. Post-stop calls to
  `Select`, `Get`, `Exec`, or `Health` now return "pool not initialised"
  instead of a pgx error from a closed pool.

**s3**
- `verifyConnectivity`: renamed the synthetic health-check bucket from
  `_samsara-health-check` to `samsara-health-probe`. Underscores are invalid
  in AWS S3 bucket names; the old name caused a 400 `InvalidBucketName`
  response on real AWS instead of the expected 404/403, making the health
  check permanently fail against AWS endpoints.

---

## grpcclient/v0.1.0 — 2026-04-08

### Added
- `Component` — samsara-compatible gRPC client backed by google.golang.org/grpc v1.71
- `Config` — target address, connect timeout, message size limits, keepalive parameters
- `WithLogger`, `WithName` options
- `AddOption(grpc.DialOption)` — inject unary/stream interceptors and other dial options
  before Start; mirrors the server component's `AddOption(grpc.ServerOption)`
- `Conn()` — exposes `*grpc.ClientConn` for passing directly to generated stub constructors
- Proactive connection: calls `conn.Connect()` then waits for `READY` state within
  `ConnectTimeout` before calling `ready()` — same fast-fail semantics as other components
- `Health` checks connectivity state: `READY` and `IDLE` are healthy; `CONNECTING`,
  `TRANSIENT_FAILURE`, and `SHUTDOWN` return errors; `IDLE` is explicitly healthy because
  gRPC re-enters it after inactivity and reconnects automatically on the next RPC
- `conn.Close()` in `Stop` with context-deadline-aware timeout logging
- `Conn()` set to nil after `Stop` so `Health` correctly reports uninitialised state
- Compile-time samsara interface assertion (no samsara import required)
- Unit tests (no server or external infra required)
- Integration tests (`//go:build integration`) with in-process gRPC servers on ephemeral
  ports; fully self-contained, no Docker services needed

---

## grpc/v0.1.0 — 2026-04-08

### Added
- `Component` — samsara-compatible gRPC server backed by google.golang.org/grpc v1.71
- `Config` — host, port, message size limits, keepalive parameters, `EnableReflection`
- `WithLogger`, `WithName` options
- `Register(RegisterFunc)` — callback-based service registration; receives `*grpc.Server`
  directly so callers use the native generated `pb.RegisterXxxServer(s, impl)` API
- `AddOption(grpc.ServerOption)` — inject unary/stream interceptors and other server
  options before Start; mirrors Fiber's `Use()` for middleware
- Built-in gRPC health service (`grpc/health/grpc_health_v1`) — always registered;
  enables Kubernetes liveness/readiness probes and `grpc-health-probe` with no caller
  configuration required
- `EnableReflection` config flag — opt-in reflection service for `grpcurl` and similar
  introspection tools; defaults to false (production-safe)
- Keepalive policy with production-safe defaults: 2 min server ping interval, 20 s ping
  timeout, 5 min max connection idle, enforcement policy preventing overly aggressive
  client pings
- `GracefulStop` with hard-stop fallback when the context deadline is exceeded during
  shutdown, preventing the supervisor from hanging
- Compile-time samsara interface assertion (no samsara import required)
- Unit tests (no server binding or external infra required)
- Integration tests (`//go:build integration`) using ephemeral ports; fully self-contained,
  no Docker services needed

---

## s3/v0.1.2 — 2026-04-08

### Fixed
- `Upload` now buffers the request body into a `*bytes.Reader` before calling
  `PutObject`, providing the seekable stream required by AWS SDK v2 to compute
  the payload checksum over plain HTTP. Previously, `detectContentType` returned
  an `io.MultiReader` (not seekable), causing all uploads to fail with
  "unseekable stream is not supported without TLS and trailing checksum".
- `ListKeys` no longer panics when `ListObjectsV2` returns a nil `IsTruncated`
  pointer. AWS always populates this field, but non-conformant S3-compatible
  servers (such as SeaweedFS) may omit it.

### Changed
- Integration tests now run against [SeaweedFS](https://github.com/seaweedfs/seaweedfs)
  (Apache 2.0) instead of LocalStack. LocalStack requires a license key as of
  late 2024; SeaweedFS is fully free, needs no account, and provides equivalent
  S3 API coverage for the operations this component uses.
- `docker-compose.yml`: replaced `localstack` service with `seaweedfs` (single-node
  `server -s3` mode) and `seaweedfs-init` (one-shot bucket creation via `weed shell`).
- `scripts/localstack-init.sh` replaced by `scripts/seaweedfs-s3.json` (static
  credentials config mounted into the SeaweedFS container).

---

## s3/v0.1.0 — 2026-04-06

Initial release of the S3 component.

### Added
- `Component` — samsara-compatible S3 component backed by AWS SDK v2
- `Config` — endpoint, region, key/secret, connect timeout, presign TTL, path-style forcing
- `WithLogger`, `WithName` options
- `Upload(ctx, UploadRequest)` — with auto content-type detection (including SVG)
- `Download(ctx, bucket, key)` — returns `io.ReadCloser`
- `Delete(ctx, bucket, key)` — single object removal
- `DeleteByPrefix(ctx, bucket, prefix)` — paginated batch delete
- `ListKeys(ctx, bucket, prefix)` — paginated key listing
- `PresignDownload(ctx, PresignRequest)` — time-limited GET URL
- `PresignUpload(ctx, PresignRequest)` — time-limited PUT URL
- `ACL` constants: Private, PublicRead, PublicReadWrite, AuthenticatedRead, BucketOwnerRead, BucketOwnerFullControl
- `PresignRequest.TTL` overrides `Config.PresignTTL` per-call
- `HeadBucket`-based connectivity check (no `ListBuckets` permission required)
- Compile-time samsara interface assertion
- Unit tests (no S3 endpoint required)
- Integration tests (`//go:build integration`) against LocalStack

---

## redis/v0.1.0 — 2026-04-06

Initial release of the Redis component.

### Added
- `Component` — samsara-compatible Redis component backed by go-redis/v9
- `Config` — host, port, DB number, credentials, connect/read/write/dial timeouts, pool size
- `WithLogger`, `WithName` options
- `Client` interface — `Set`, `Get`, `Del`, `Exists`, `Expire`, `TTL`, `Scan`
- `ErrNil` sentinel (aliases `redis.Nil`) for missing-key detection
- Cursor-based `Scan` (safe for large key spaces; avoids `KEYS`)
- Compile-time samsara interface assertion
- Unit tests (no server required)
- Integration tests (`//go:build integration`) against the existing Redis service

---

## rabbitmq/v0.1.1 — 2026-04-05

Fix possible shutdown leaks.

### Fixed
- The dial goroutine may still succeed after we return. Drain the channel in a goroutine and close any connection it produces so we don't leak an open TCP connection to the broker.

---

## fiber/v0.1.0 — 2026-04-05

Initial release of the Fiber HTTP server component.

### Added
- `Component` — samsara-compatible Fiber v3 HTTP server
- `Config` — structured config: host, port, path prefix, body limit, CORS, timeouts, error handler, logger format, security headers
- `WithLogger`, `WithName`, `WithSwagger` options
- `Register(RegisterFunc)` — callback-based route registration; safe before and after `Start`
- `Use(...)` — global middleware injection before domain routes
- `DefaultErrorHandler` with `HTTPStatuser` interface for library-agnostic error mapping
- `ErrorResponse` JSON shape
- `RealIP`, `ExcludeRoutes`, `Route`, `SkipperFunc` helper utilities
- Built-in middleware stack: recover, CORS, security headers, compress, request logger
- Built-in `GET {PathPrefix}/health` endpoint (204, excluded from access logs)
- Compile-time samsara interface assertion
- Unit tests (no server binding required)
- Integration tests (`//go:build integration`) using ephemeral ports

---

## rabbitmq/v0.1.0 — 2026-04-05

Initial release of the RabbitMQ component.

### Added
- `Component` — samsara-compatible AMQP component backed by amqp091-go
- `Config` — structured config with individual fields and `URI` override; credentials percent-encoded
- `WithLogger`, `WithName` options
- `DeclareExchange(name, kind, durable)` — registered and re-declared on restart
- `Subscribe(exchange, queue, handler)` — queue binding with routing key = queue name
- `SubscribeWithKey(exchange, queue, routingKey, handler)` — explicit routing key for topic patterns
- `Publish(ctx, exchange, routingKey, contentType, body)` — context-aware publish
- `PublishWithType(...)` — publish with AMQP message type field
- `ExchangeKind` constants: Direct, Topic, Fanout, Headers
- `ContentType` constants: JSON, JSON+UTF8, Text, Bytes
- Context-aware dial: races `amqp.DialConfig` against `ConnectTimeout` and `ctx`
- Consumer goroutines tied to component context; exit cleanly on Stop/restart
- Compile-time samsara interface assertion
- Unit tests (no broker required)
- Integration tests (`//go:build integration`) against a live RabbitMQ instance

---

## postgresql/v0.1.0 — 2026-04-04

Initial release of the PostgreSQL component.

### Added
- `Component` — samsara-compatible lifecycle wrapper around a `pgxpool.Pool`
- `Config` — structured config with individual fields and `URI` override
- `WithLogger`, `WithName` options
- `DB` interface — `Select`, `Get`, `Exec`, `BeginTx`, `CommitTx`
- `TxFinaliser` interface for stub-based transaction testing
- `ErrNoRows` sentinel (aliases `pgx.ErrNoRows`)
- Compile-time samsara interface assertion
- Unit tests (race detector, count=3, no database required)
- Integration tests (`//go:build integration`) against a live Postgres instance
- `docker-compose.yml` with ephemeral Postgres, Redis, RabbitMQ
- `Makefile` with `check`, `test-race`, `coverage`, `test-integration`, `tidy`
- GitHub Actions CI: unit + static analysis + integration jobs
