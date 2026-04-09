# Changelog

All notable changes to samsara-components are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Each component module is versioned independently; this file tracks changes
across all of them.

---

## [Unreleased]

---

## grpc/v0.1.1, grpcclient/v0.1.1, fiber/v0.1.2, rabbitmq/v0.1.3, postgresql/v0.1.2, s3/v0.1.4 — 2026-04-09

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

**rabbitmq**
- Consumer goroutine leak on stop: `SubscribeWithKey`'s live-bind path
  previously passed `context.Background()` to `bindAndConsume`, so consumer
  goroutines started via post-`Start` `Subscribe` calls never exited on stop
  or restart. `runCtx`/`runCancel` fields (guarded by `mu`) now store the
  lifecycle context of the current `Start` call. `Stop` calls `runCancel`
  before closing the connection; `Start` calls it in its terminal block.
  `SubscribeWithKey` reads `runCtx` under `RLock` and passes it to
  `bindAndConsume`.

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