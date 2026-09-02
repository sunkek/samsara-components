# redis

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/redis.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/redis)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/redis)](https://goreportcard.com/report/github.com/sunkek/samsara-components/redis)

A [samsara](https://github.com/sunkek/samsara)-compatible Redis component
backed by [go-redis/v9](https://github.com/redis/go-redis).

```
go get github.com/sunkek/samsara-components/redis
```

---

## Usage

### Register with a supervisor

```go
rdb := redis.New(redis.Config{
    Host: "localhost",
    Port: 6379,
})
sup.Add(rdb,
    samsara.WithTier(samsara.TierCritical),
    samsara.WithRestartPolicy(samsara.ExponentialBackoff(5, time.Second)),
)
```

### Use in domain adapters

Depend on the `KV` interface, not `*Component`, to keep adapters
testable without a real Redis server:

```go
type SessionStore struct {
    rdb redis.KV
}

func (s *SessionStore) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
    return s.rdb.Set(ctx, "session:"+id, data, ttl)
}

func (s *SessionStore) Load(ctx context.Context, id string) (string, error) {
    val, err := s.rdb.Get(ctx, "session:"+id)
    if errors.Is(err, redis.ErrNil) {
        return "", ErrNotFound
    }
    return val, err
}
```

---

## Configuration

```go
redis.Config{
    Host string        // default: "localhost"
    Port int           // default: 6379
    DB   int           // default: 0
    User string        // ACL username; leave empty for password-only auth
    Pass string        // password or ACL user password

    ConnectTimeout time.Duration // default: 10s — startup PING deadline
    DialTimeout    time.Duration // default: go-redis default (5s)
    ReadTimeout    time.Duration // default: go-redis default (3s)
    WriteTimeout   time.Duration // default: go-redis default (ReadTimeout)
    PoolSize       int           // default: 10 per CPU

    TLS                   bool   // enable TLS; gates all TLS_* fields below
    TLSCAFile             string // PEM CA bundle for server verification (optional; system trust if empty)
    TLSCertFile           string // client cert for mTLS (paired with TLSKeyFile)
    TLSKeyFile            string // client private key for mTLS (paired with TLSCertFile)
    TLSServerName         string // SNI / hostname verification target; defaults to Host
    TLSInsecureSkipVerify bool   // disable cert verification (dev/debug only)
    TLSMinVersion         string // "1.2" (default) or "1.3"

    OnOperation func(op string, d time.Duration, err error) // default: nil — see Metrics
}
```

### TLS

```go
rdb := redis.New(redis.Config{
    Host:      "rag_redis",
    Port:      16379,
    User:      "sber_kb",
    Pass:      os.Getenv("REDIS_PASS"),
    TLS:       true,
    TLSCAFile: "/tls/ca.crt",
    // TLSServerName: "rag_redis", // defaults to Host
})
```

Server with `tls-auth-clients no` accepts the connection without a client
certificate — leave `TLSCertFile` / `TLSKeyFile` empty. Both must be set
together when mutual TLS is required. A misconfigured TLS block (bad CA,
unknown `TLSMinVersion`, half-set cert/key) causes `Start` to fail loudly;
there is no plaintext fallback.

### Options

```go
redis.WithLogger(slog.Default())    // attach a structured logger
redis.WithName("session-store")     // override component name
```

---

## Escape hatch

`Client() *redis.Client` returns the go-redis handle for features the `KV`
interface does not cover — pipelines, Lua `EVAL`, hashes, sets, streams,
pub/sub.

```go
h := rdb.Client() // nil before Start and after Stop
pipe := h.Pipeline()
```

Adapters should keep depending on `KV`; this is the long tail that interface
deliberately does not cover. See
[ADR-0005](../docs/adr/0005-driver-escape-hatch-accessors.md).

### Driver options

`AddOption` appends a `redis.Options` mutator applied when the client is built
in `Start` — the settings `Config` does not model.

```go
rdb.AddOption(func(o *redis.Options) {
    o.MaxRetries = 5
})
```

Call it before `Start`. Options are kept and re-applied on every restart, after
the component's own settings, so a mutator can override `Config`.

---

## API reference

### `KV` interface

| Method | Description |
|--------|-------------|
| `Set(ctx, key, value, ttl)` | Store a value; use `ttl=0` for no expiry |
| `Get(ctx, key)` | Retrieve a string value; returns `ErrNil` if absent |
| `Del(ctx, keys...)` | Delete one or more keys; returns count removed |
| `Exists(ctx, keys...)` | Count how many of the given keys exist |
| `Incr(ctx, key)` | Atomically increment an integer; first call returns 1 |
| `Expire(ctx, key, ttl)` | Set a TTL on an existing key |
| `TTL(ctx, key)` | Get remaining TTL; negative if absent or no expiry |
| `Scan(ctx, pattern)` | Iterate all matching keys safely (cursor-based) |

`*Component` satisfies `KV`.

### Sentinel errors

```go
errors.Is(err, redis.ErrNil)      // key does not exist (Get returns this)
errors.Is(err, redis.ErrNotReady) // no live connection; the call was not attempted
```

---

## Health checking

`*Component` implements `samsara.HealthChecker`. The supervisor polls
`Health(ctx)` every health interval and sends a PING to verify the server
is reachable. No configuration required.

---

## Multiple instances

```go
cache   := redis.New(cfg.Cache,   redis.WithName("redis-cache"))
sessions := redis.New(cfg.Session, redis.WithName("redis-sessions"))

sup.Add(cache,    samsara.WithTier(samsara.TierSignificant))
sup.Add(sessions, samsara.WithTier(samsara.TierCritical))
```

---

## Testing adapters without Redis

```go
type mockRedis struct{ redis.KV }

func (m *mockRedis) Get(_ context.Context, key string) (string, error) {
    if key == "session:abc" {
        return `{"user_id":1}`, nil
    }
    return "", redis.ErrNil
}
```

---

## Metrics

`Config.OnOperation` is called once per completed `KV` operation with a fixed
operation name, how long the call took, and the error it returned. It defaults to
nil, which disables reporting entirely.

```go
c := redis.New(redis.Config{
    OnOperation: func(op string, d time.Duration, err error) {
        // op is one of: redis.set, redis.get, redis.del, redis.exists, redis.incr, redis.expire, redis.ttl, redis.scan
        metrics.Observe(op, d, err)
    },
})
```

`ErrNotReady` is not an operation failure: it means there was no live handle and
the call was never attempted. Those are reported with a **zero duration**, so
they never enter the latency distribution. Classify accordingly before counting
error rates. `redis.ErrNil` (missing key) is likewise a
miss, not a failure.

Work done through the escape hatch (``Client()``) is **not** measured.

See [ADR-0006](../docs/adr/0006-metrics-behind-the-narrow-interface.md).
