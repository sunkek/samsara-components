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

Depend on the `Client` interface, not `*Component`, to keep adapters
testable without a real Redis server:

```go
type SessionStore struct {
    rdb redis.Client
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
}
```

### Options

```go
redis.WithLogger(slog.Default())    // attach a structured logger
redis.WithName("session-store")     // override component name
```

---

## API reference

### `Client` interface

| Method | Description |
|--------|-------------|
| `Set(ctx, key, value, ttl)` | Store a value; use `ttl=0` for no expiry |
| `Get(ctx, key)` | Retrieve a string value; returns `ErrNil` if absent |
| `Del(ctx, keys...)` | Delete one or more keys; returns count removed |
| `Exists(ctx, keys...)` | Count how many of the given keys exist |
| `Expire(ctx, key, ttl)` | Set a TTL on an existing key |
| `TTL(ctx, key)` | Get remaining TTL; negative if absent or no expiry |
| `Scan(ctx, pattern)` | Iterate all matching keys safely (cursor-based) |

`*Component` satisfies `Client`.

### Sentinel errors

```go
errors.Is(err, redis.ErrNil) // key does not exist (Get returns this)
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
type mockRedis struct{ redis.Client }

func (m *mockRedis) Get(_ context.Context, key string) (string, error) {
    if key == "session:abc" {
        return `{"user_id":1}`, nil
    }
    return "", redis.ErrNil
}
```
