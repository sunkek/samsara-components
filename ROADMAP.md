# samsara-components — ROADMAP

Gap-closing backlog derived from a production review of the `fiber`, `postgresql`,
`redis`, and `s3` components as consumed by an external Fiber gateway. The shared
shape is good: `New(Config, WithLogger/WithName)`, connectivity-probe-before-`ready()`,
ctx-bounded idempotent `Stop`, narrow driver-independent interfaces (`DB`, `Client`),
compile-time samsara-interface assertions. The items below are the rough edges — one
of which (s3 in-memory upload buffering) caused a production incident in the consuming
gateway (health-probe starvation under concurrent large uploads → supervisor
self-restart).

Line references are against the current `main`. Priority: s3 first (prod impact),
then cross-cutting, then per-component.

---

## Cross-cutting

### X1. No metrics / OpenTelemetry in any component
Observability is limited to a 2-method `Info`/`Error` logger. No component exports
request counts, latencies, pool stats, or spans. Consumers can't see connection-pool
saturation, per-op latency, or error rates without wrapping every call themselves.
**Direction.** Optional metrics hook per component (or a shared helper) emitting
Prometheus/OTel; keep the dependency behind a build tag / subpackage.

### X2. No in-place retry/backoff
Components fail `Start` on the first transient connectivity blip and lean entirely on
the samsara supervisor's restart policy for recovery. A brief DNS/network hiccup at
boot escalates to a full component restart cycle. **Direction.** Optional bounded
connect-retry inside `Start` before giving up.

### X3. No escape hatch for raw driver options
Only `WithLogger`/`WithName` are exposed. Anything the `Config` struct doesn't model
is unreachable. The grpc/grpcclient components already solved this with `AddOption`;
port that pattern to fiber/postgresql/redis/s3 so callers can pass native driver
options without a wrapper change.

### X4. Ship a concrete `samsara.MetricsObserver` implementation
(Moved from the samsara core ROADMAP — C1. Belongs here, not in core: core is
single-package zero-dependency and must not pull a Prometheus/OTel client.)
The `samsara.MetricsObserver` interface exists with only a nop default, so every
consumer hand-builds one — the reviewed gateway wrote a full Prometheus observer +
registry + exposition server mirroring `HealthServer`. This is the single strongest
"missing battery" signal. **Direction.** Provide an optional Prometheus (and/or
OTel) `MetricsObserver` that exports component up/restart/health-check-duration
metrics, so wiring supervisor telemetry is one `WithMetricsObserver(...)` call. Keep
it behind a build tag / subpackage (as X1) so the dependency is opt-in. Distinct
from X1: X1 is per-component request/pool telemetry; X4 is supervisor-lifecycle
telemetry via the core's observer hook.

---

## s3 (highest priority — caused prod incident)

### S1. `Upload` buffers the entire body into RAM
`Upload` does `io.ReadAll(r.Body)` and wraps it in a `bytes.Reader`
(`s3/operations.go:88`) for every upload — no streaming, no multipart path. Under
concurrent large uploads the consuming gateway spiked to full-body-size RAM per
in-flight request, starved its health probe, and was restarted by samsara.
**Direction.** Add a streaming upload path (pass the `io.Reader` through to the SDK)
and a multipart-upload path for large objects; keep the buffered path only where a
seekable body is genuinely required (the SDK-v2 plain-HTTP checksum workaround).
**Acceptance.** Uploading an N-GB object holds O(part size), not O(N), in memory.

### S2. No `*s3.Client` accessor and a thin `UploadRequest`
`getClient` is private (`s3/s3.go:150`) and `UploadRequest` (`s3/operations.go:17`)
has no fields for SSE, object metadata, tagging, or explicit `Content-Length`.
`CopyObject`, `HeadObject`, range-GET, versioning, and multipart are all unreachable.
**Direction.** Either broaden the op surface or export a `Client() *s3.Client`
accessor (as done for other drivers below), plus metadata/SSE/tagging fields on
`UploadRequest`.

### S3. `Health` HeadBuckets a synthetic bucket and treats 403 as healthy
`Health` re-runs `verifyConnectivity` (`s3/s3.go:251,259`), which issues
`HeadBucket` against a synthetic bucket name (`s3/s3.go:268`) every probe interval.
This adds a request per health check and, because it accepts 403 as "reachable,"
reports healthy even when the credential is mis-scoped for the buckets the app
actually uses. **Direction.** Health-check a configured real bucket (or make the
probe bucket/behaviour configurable) and distinguish auth failure from
connectivity.

---

## redis

### R1. `Client` interface is too thin for anything beyond simple KV
The interface (`redis/client.go:17`) exposes 8 ops — `Set`, `SetNX`, `Get`, `Del`,
`Exists`, `Expire`, `TTL`, `Scan`. No `INCR`/`DECR`, no pipelines, no Lua `EVAL`, no
pub/sub, no hashes/sets/zsets/streams, no `MSET`/`MGET`, and no `*redis.Client`
accessor. **Concrete downstream cost:** the consuming gateway's rate limiter cannot
express an atomic `INCR`+`EXPIRE` and falls back to a racy read-modify-write `Set`
(gateway `internal/component/ratelimit/ratelimit.go:112`). `SetNX` (added recently)
helps locks/idempotency but is not enough for counters. **Direction.** Add `Incr`/
`IncrBy` + `Expire`-on-first (or a Lua helper), and/or export `Client() *redis.Client`
for advanced use.

### R2. No cluster / sentinel / failover support
`Start` builds a plain `redis.NewClient` (`redis/redis.go:212`); `Config` has no
cluster addrs or sentinel/failover config. **Direction.** Optional
`ClusterClient`/`FailoverClient` modes selected via config.

### R3. Limited pool tuning
Only `PoolSize` (`redis/redis.go:58`). No `MinIdleConns`, `PoolTimeout`,
`ConnMaxLifetime`, `ConnMaxIdleTime`, or client-level `MaxRetries`. **Direction.**
Surface the go-redis pool knobs on `Config`.

### R4. `Scan` batches at a fixed size and eagerly materialises the whole match set
`Scan` (`redis/client.go:172`) returns a `[]string` of every match — a large
keyspace scan is unbounded memory. **Direction.** Add a streaming/callback variant
(`ScanFunc(ctx, pattern, func(key) error)`) and/or a configurable batch count.

---

## postgresql

### P1. No exported `Pool()` accessor
`getPool` is private (`postgresql/postgresql.go:183`); the `DB` interface covers
`Select`/`Get`/`Exec`/`BeginTx` only. `CopyFrom` (bulk load), `LISTEN`/`NOTIFY`,
batched queries, and custom row handling are unreachable. **Direction.** Export
`Pool() *pgxpool.Pool` (matching the accessor pattern proposed for redis/s3).

### P2. Minimal pool tuning
Only `MaxConns`/`MinConns` (`postgresql/postgresql.go:56-59`). No
`MaxConnLifetime`, `MaxConnIdleTime`, `MaxConnLifetimeJitter`, `HealthCheckPeriod`,
or `AfterConnect`/`BeforeAcquire` hooks despite pgxpool supporting them.
**Direction.** Surface these on `Config`.

### P3. Shallow health, no pool stats exposed
`Health` is a single `pool.Ping` (`postgresql/postgresql.go:283,288`) — it says
nothing about pool saturation or waiting acquires, and `pool.Stat()` is not exposed.
**Direction.** Expose `Stat()` (feeds X1) and optionally fail health on sustained
acquire-wait saturation.

### P4. TLS only via SSLMode / URI
No cert-file config surface (the redis component has one via its TLS block). Callers
needing client certs must hand-build the DSN. **Direction.** Add a TLS config block
mirroring redis.

---

## fiber

### F1. No `*gf.App` accessor; routes must be registered before `Start`
`Register`/`Use` are the only injection points and must run pre-`Start`; there is no
accessor to the underlying `*fiber.App`, so any Fiber feature the component doesn't
surface is unreachable. **Direction.** Export `App() *fiber.App` (guarded/documented
for pre-Start use).

### F2. No built-in metrics/tracing middleware
Only a request-logger format string; no Prometheus/OTel middleware. Consumers
re-implement HTTP metrics (the gateway did). **Direction.** Optional metrics/tracing
middleware (ties into X1).

### F3. Health probe is an HTTP round-trip to the component's own advertised port
`Health` (`fiber/fiber.go:419`) dials the advertised address over loopback each
probe — an extra socket round-trip per check that also assumes the bind address is
loopback-dialable from inside the pod. **Direction.** Prefer an in-process readiness
signal (the `OnListen` hook already fires at bind, `fiber/fiber.go:379`) over a real
HTTP GET.

### F4. Hardcoded compress level, coarse CORS, missing listener knobs
Compress level is hardcoded `LevelBestSpeed` (`fiber/fiber.go:316`). CORS lacks
`AllowCredentials`/`ExposeHeaders`/`MaxAge`. No TLS/HTTP2 listener config, no
per-route body limit. **Direction.** Make compress level configurable; expand the
CORS knobs; add listener TLS config.

---

## Candidate new components (infra consumers keep hand-rolling)

Observed in the reviewed gateway — each was built from scratch because no component
exists:

- **OIDC / JWKS validation component** — JWKS cache + JWT validation + backchannel-
  logout revocation. (gateway `internal/component/keycloak/*`)
- **Supervised HTTP-upstream component with health** — the gateway's RAG and
  ld-index clients are plain `net/http`, unsupervised, so an unreachable backend
  never flips readiness. A generic outbound-HTTP component (base URL, auth injection,
  status→error mapping, health check, restart policy) would be broadly reusable.
- **Rate limiter** — Redis-backed limiter with tiers + fail-open. (gateway
  `internal/component/ratelimit/*`) Blocked in part on redis R1 (atomic counters).

---

## Notes on current state
The reviewed gateway, after upgrading, pins current releases: `s3` v0.1.5 (the
`Stop` stale-client race is fixed there — not an open item), `redis` v0.3.0 (has
`SetNX`), `fiber` v0.3.0, `postgresql` v0.1.1. The `rabbitmq`, `grpc`, and
`grpcclient` components exist but were not exercised by this review.
