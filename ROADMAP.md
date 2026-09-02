# samsara-components — ROADMAP

Gap-closing backlog derived from a production review of the `fiber`, `postgresql`,
`redis`, and `s3` components as consumed by an external Fiber gateway. The shared
shape is good: `New(Config, WithLogger/WithName)`, connectivity-probe-before-`ready()`,
ctx-bounded idempotent `Stop`, narrow driver-independent interfaces (`DB`, `KV`),
compile-time samsara-interface assertions. The items below are the rough edges — one
of which (s3 in-memory upload buffering) caused a production incident in the consuming
gateway (health-probe starvation under concurrent large uploads → supervisor
self-restart).

Line references are against the current `main`. Priority: s3 first (prod impact),
then cross-cutting, then per-component.

**Status markers.** An item carries a `Status:` line once its state has been
verified against the code. Items with no marker were not re-verified in the last
pass — treat them as unconfirmed rather than open.

---

## Cross-cutting

### X1. No metrics / OpenTelemetry in any component
**Status: partly shipped** — the per-operation half landed 2026-08-28 in
`redis/v0.7.0`, `postgresql/v0.4.0`, `s3/v0.4.0`, `sqlite/v0.2.0` and
`rabbitmq/v0.5.0`: `Config.OnOperation` reports op name, duration and error
once per call through the narrow interface, per
[ADR-0006](./docs/adr/0006-metrics-behind-the-narrow-interface.md). `fiber`,
`grpc`, `grpcclient` and `prometheus` are deliberately not instrumented — none
has a per-operation caller surface, and serving components want request
middleware instead (see F2). What remains open: pool-saturation and
connection-count gauges, spans, and the middleware half.

(Premise corrected 2026-08-26 — every module declares a 4-method `Logger`:
`Debug`/`Info`/`Warn`/`Error`, not a 2-method one. The observability gap below
is unaffected.)

Observability is limited to a per-component `Logger`. No component exports
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
**Status: shipped** for postgresql, redis and s3 in `postgresql/v0.5.0`,
`redis/v0.8.0` and `s3/v0.6.0` (2026-09-02): each takes a native mutator
(`func(*pgxpool.Config)`, `func(*redis.Options)`, `func(*s3.Options)`) applied
when the handle is built in `Start`, re-applied on every restart. `fiber` is
left out on purpose — it builds its app from a `fiber.Config` value, not option
functions, so the pattern does not port; a fiber escape hatch would be a
different design and is not covered by this item.

Only `WithLogger`/`WithName` are exposed. Anything the `Config` struct doesn't model
is unreachable. The grpc/grpcclient components already solved this with `AddOption`;
port that pattern to fiber/postgresql/redis/s3 so callers can pass native driver
options without a wrapper change.

### X4. Ship a concrete `samsara.MetricsObserver` implementation
**Status: shipped** in `prometheus/v0.1.0` (2026-07-20). `prometheus.Observer`
exports component up / starts / restarts / stop-errors / health-check-duration /
health-check-failures, and wiring is the single
`samsara.WithMetricsObserver(comp.Observer())` call this item asked for. Kept
here for the record; nothing further to do.

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
**Status: shipped** — [#4](https://github.com/sunkek/samsara-components/issues/4).

`Upload` did `io.ReadAll(r.Body)` and wrapped it in a `bytes.Reader` for every
upload — no streaming, no multipart path. Under concurrent large uploads the
consuming gateway spiked to full-body-size RAM per in-flight request, starved its
health probe, and was restarted by samsara.

Uploads now stream through `feature/s3/transfermanager`, held behind an
unexported `uploadEngine` port ([ADR-0004](./docs/adr/0004-transfermanager-behind-an-internal-port.md)).
Peak memory is `(UploadConcurrency+1) x UploadPartSize`, independent of object
size; the body need be neither seekable nor of known length. The
"seekable body" constraint the old code cited was real but was satisfiable
without buffering — see
[the research note](./docs/research/aws-sdk-v2-s3-streaming-and-checksums.md).

### S2. A thin `UploadRequest`
**Status: partly shipped** — the accessor landed; `UploadRequest` is unchanged.

`Client() *s3.Client` now reaches `CopyObject`, `HeadObject`, range-GET and
versioning, per [ADR-0005](./docs/adr/0005-driver-escape-hatch-accessors.md);
multipart is no longer a gap either, since `Upload` streams through the transfer
manager. What remains is `UploadRequest` (`s3/operations.go`), which still has no
fields for SSE, object metadata, tagging, or explicit `Content-Length`.
**Direction.** Add those fields for the uses common enough to deserve wrapping,
rather than pushing every caller through the accessor.

### S3. `Health` HeadBuckets a synthetic bucket and treats 403 as healthy
**Status: shipped** in `s3/v0.5.0` (2026-09-02). `Config.HealthBucket` points
the probe at a real bucket and makes it strict — 403 becomes
`ErrProbeForbidden`, 404 becomes `ErrProbeBucketMissing`, in `Start` as well as
`Health`. Empty keeps the old synthetic probe, so the change is additive. The
per-probe request count is unchanged (one `HeadBucket`); making the probe
optional or cheaper was not part of this item.

`Health` re-runs `verifyConnectivity` (`s3/s3.go`), which issues
`HeadBucket` against a synthetic bucket name (`s3/s3.go`) every probe interval.
This adds a request per health check and, because it accepts 403 as "reachable,"
reports healthy even when the credential is mis-scoped for the buckets the app
actually uses. **Direction.** Health-check a configured real bucket (or make the
probe bucket/behaviour configurable) and distinguish auth failure from
connectivity.

---

## redis

### R1. `KV` interface is too thin for anything beyond simple key/value work
**Status: partly shipped.** The atomic-counter half landed in `redis/v0.5.0`
(2026-07-28): `Incr` plus `Expire`-on-first is exactly the `INCR`+`EXPIRE`
pattern the rate limiter needed, and `SetNX` landed earlier for locks and
idempotency keys. What remains open is everything below except `INCR`.

The `KV` interface (`redis/client.go`) exposes 9 ops — `Set`, `SetNX`, `Get`, `Del`,
`Exists`, `Incr`, `Expire`, `TTL`, `Scan`. No `DECR`, no pipelines, no Lua `EVAL`, no
pub/sub, no hashes/sets/zsets/streams, no `MSET`/`MGET` — though all of them are
now reachable through `Client()`. **Historical downstream cost (now resolved):** the consuming gateway's rate
limiter could not express an atomic `INCR`+`EXPIRE` and fell back to a racy
read-modify-write `Set` (gateway
`internal/component/ratelimit/ratelimit.go`). `Incr` closed that.
**Direction for the remainder.** `Client() *redis.Client` shipped, per
[ADR-0005](./docs/adr/0005-driver-escape-hatch-accessors.md). Still open: add
`DecrBy`/`IncrBy`, pipelines, or a Lua helper as concrete needs appear.

### R2. No cluster / sentinel / failover support
`Start` builds a plain `redis.NewClient` (`redis/redis.go`); `Config` has no
cluster addrs or sentinel/failover config. **Direction.** Optional
`ClusterClient`/`FailoverClient` modes selected via config.

### R3. Limited pool tuning
Only `PoolSize` (`redis/redis.go`). No `MinIdleConns`, `PoolTimeout`,
`ConnMaxLifetime`, `ConnMaxIdleTime`, or client-level `MaxRetries`. **Direction.**
Surface the go-redis pool knobs on `Config`.

### R4. `Scan` batches at a fixed size and eagerly materialises the whole match set
`Scan` (`redis/client.go`) returns a `[]string` of every match — a large
keyspace scan is unbounded memory. **Direction.** Add a streaming/callback variant
(`ScanFunc(ctx, pattern, func(key) error)`) and/or a configurable batch count.

---

## postgresql

### P1. No exported `Pool()` accessor
**Status: shipped** — `Pool() *pgxpool.Pool`, per
[ADR-0005](./docs/adr/0005-driver-escape-hatch-accessors.md).

The `DB` interface covers `Select`/`Get`/`Exec`/`BeginTx` only, so `CopyFrom`
(bulk load), `LISTEN`/`NOTIFY`, batched queries and custom row handling were
unreachable. The accessor reaches all of them.

### P2. Minimal pool tuning
Only `MaxConns`/`MinConns` (`postgresql/postgresql.go`). No
`MaxConnLifetime`, `MaxConnIdleTime`, `MaxConnLifetimeJitter`, `HealthCheckPeriod`,
or `AfterConnect`/`BeforeAcquire` hooks despite pgxpool supporting them.
**Direction.** Surface these on `Config`.

### P3. Shallow health, no pool stats exposed
`Health` is a single `pool.Ping` (`postgresql/postgresql.go`) — it says
nothing about pool saturation or waiting acquires, and `pool.Stat()` is not exposed.
**Direction.** Expose `Stat()` (feeds X1) and optionally fail health on sustained
acquire-wait saturation.

### P4. TLS only via SSLMode / URI
No cert-file config surface (the redis component has one via its TLS block). Callers
needing client certs must hand-build the DSN. **Direction.** Add a TLS config block
mirroring redis.

---

## fiber

### F1. Fiber features outside `Register`/`Use` are unreachable
**Status: partly shipped** — `App() *fiber.App` landed, per
[ADR-0005](./docs/adr/0005-driver-escape-hatch-accessors.md), but it only solves
the reading half.

The app is built inside `Start`, so `App` returns nil until Fiber is already
listening, and anything registered through it afterwards is not served.
`Register`/`Use` remain the only way to add behaviour, and they take a
`gf.Router`, not the app — so app-level features stay out of reach.

**Direction.** `RegisterApp(func(*fiber.App))`, stored like the existing
`RegisterFunc`s and replayed inside `Start` with the built app before it
listens. Deferred until something actually needs it: as of 2026-08-27 the
reviewed gateway registers everything through `Register(func(r gf.Router))` and
configures its one app-level concern, `ErrorHandler`, through `Config`.

### F2. No built-in metrics/tracing middleware
Only a request-logger format string; no Prometheus/OTel middleware. Consumers
re-implement HTTP metrics (the gateway did). **Direction.** Optional metrics/tracing
middleware (ties into X1).

### F3. Health probe is an HTTP round-trip to the component's own advertised port
`Health` (`fiber/fiber.go`) dials the advertised address over loopback each
probe — an extra socket round-trip per check that also assumes the bind address is
loopback-dialable from inside the pod. **Direction.** Prefer an in-process readiness
signal (the `OnListen` hook already fires at bind, `fiber/fiber.go`) over a real
HTTP GET.

### F4. Hardcoded compress level, coarse CORS, missing listener knobs
Compress level is hardcoded `LevelBestSpeed` (`fiber/fiber.go`). CORS lacks
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
  `internal/component/ratelimit/*`) No longer blocked: `redis.Incr` plus
  `Expire`-on-first landed in `redis/v0.5.0`.

---

## Notes on current state
The reviewed gateway, after upgrading, pins current releases: `s3` v0.1.5 (the
`Stop` stale-client race is fixed there — not an open item), `redis` v0.3.0 (has
`SetNX`; `Incr` arrived later in v0.5.0), `fiber` v0.3.0, `postgresql` v0.1.1. The `rabbitmq`, `grpc`, and
`grpcclient` components exist but were not exercised by this review.
