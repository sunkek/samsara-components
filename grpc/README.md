# grpc

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/grpc.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/grpc)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/grpc)](https://goreportcard.com/report/github.com/sunkek/samsara-components/grpc)

A [samsara](https://github.com/sunkek/samsara)-compatible gRPC server component
backed by [google.golang.org/grpc](https://github.com/grpc/grpc-go).

```
go get github.com/sunkek/samsara-components/grpc
```

---

## Usage

### Register with a supervisor

```go
import (
    sc "github.com/sunkek/samsara-components/grpc"
    grpclib "google.golang.org/grpc"
)

srv := sc.New(sc.Config{
    Host: "0.0.0.0",
    Port: 9090,
})
sup.Add(srv, samsara.WithTier(samsara.TierCritical))
```

### Register service implementations

Pass a `RegisterFunc` to `Register`. It receives the live `*grpc.Server` and
calls the generated registration function on it:

```go
srv.Register(func(s *grpclib.Server) {
    pb.RegisterGreeterServer(s, &greeterImpl{db: db})
    pb.RegisterUserServiceServer(s, &userImpl{db: db})
})
```

Domain adapters should call `srv.Register` in their wiring function, before
`app.Run()`:

```go
// In your adapter:
func (a *UserAdapter) RegisterGRPC(srv *sc.Component) {
    srv.Register(func(s *grpclib.Server) {
        pb.RegisterUserServiceServer(s, a)
    })
}

// In main.go:
userAdapter := user.New(db)
userAdapter.RegisterGRPC(srv)
```

`Register` is safe to call before or after `Start`. RegisterFuncs are called in
registration order and re-applied on every restart.

---

## Interceptors

Use `AddOption` to inject unary and stream interceptors — for auth, logging,
tracing, metrics, and so on. Must be called before `Start`.

```go
// Single interceptor:
srv.AddOption(grpclib.ChainUnaryInterceptor(authInterceptor))

// Multiple interceptors execute left to right:
srv.AddOption(grpclib.ChainUnaryInterceptor(
    authInterceptor,
    loggingInterceptor,
    tracingInterceptor,
))
srv.AddOption(grpclib.ChainStreamInterceptor(
    authStreamInterceptor,
    loggingStreamInterceptor,
))
```

A unary interceptor has this signature:

```go
func myInterceptor(
    ctx context.Context,
    req any,
    info *grpclib.UnaryServerInfo,
    handler grpclib.UnaryHandler,
) (any, error) {
    // before handler
    resp, err := handler(ctx, req)
    // after handler
    return resp, err
}
```

A stream interceptor:

```go
func myStreamInterceptor(
    srv any,
    ss grpclib.ServerStream,
    info *grpclib.StreamServerInfo,
    handler grpclib.StreamHandler,
) error {
    // before handler
    return handler(srv, ss)
}
```

---

## Reflection

Set `EnableReflection: true` in development or staging to allow tools like
`grpcurl` to introspect your server's API without the `.proto` files:

```go
srv := sc.New(sc.Config{
    Host:             "0.0.0.0",
    Port:             9090,
    EnableReflection: os.Getenv("ENV") != "production",
})
```

Then introspect or call methods without proto files:

```bash
# List all services
grpcurl -plaintext localhost:9090 list

# Describe a service
grpcurl -plaintext localhost:9090 describe myapp.UserService

# Call a method
grpcurl -plaintext -d '{"id": "abc"}' localhost:9090 myapp.UserService/GetUser
```

Keep `EnableReflection` off in production — it exposes your full API surface
to anyone who can reach the port.

---

## Built-in health service

The standard [gRPC health protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md)
is always registered, with no configuration required. This enables:

- Kubernetes liveness and readiness probes via `grpc-health-probe`
- Load balancer health checks
- The `grpcurl` health command

```bash
# Check health with grpcurl
grpcurl -plaintext localhost:9090 grpc.health.v1.Health/Check

# Or with grpc-health-probe
grpc-health-probe -addr=localhost:9090
```

Kubernetes probe configuration:

```yaml
livenessProbe:
  grpc:
    port: 9090
readinessProbe:
  grpc:
    port: 9090
```

---

## Configuration

```go
sc.Config{
    Host string        // default: "0.0.0.0"
    Port int           // default: 9090

    EnableReflection bool // default: false — enable only in dev/staging

    MaxRecvMsgSizeMB int  // default: 4  — max inbound message size
    MaxSendMsgSizeMB int  // default: 4  — max outbound message size

    // Server keepalive (how the server pings idle clients)
    KeepaliveTime     time.Duration // default: 2 min
    KeepaliveTimeout  time.Duration // default: 20 s

    // Connection lifetime
    MaxConnectionIdle time.Duration // default: 5 min (0 = unlimited)
    MaxConnectionAge  time.Duration // default: 0 (disabled)
}
```

### Options

```go
sc.WithLogger(slog.Default())    // attach a structured logger
sc.WithName("internal-grpc")     // override component name (useful for multiple servers)
```

---

## Health checking

`*Component` implements `samsara.HealthChecker`. The supervisor polls
`Health(ctx)` every health interval. Health fails if the server is not
currently serving (not yet started, or shutting down).

This is a lightweight in-process check. For a full wire-level health probe,
use the built-in gRPC health service directly (see above).

---

## Graceful shutdown

On `Stop`, the component calls `GracefulStop`, which:
1. Stops accepting new connections and RPCs
2. Waits for all in-flight RPCs to complete
3. Closes all open connections

If the context deadline is exceeded before all RPCs finish, `Stop` falls back
to `grpc.Server.Stop()` (force-close), ensuring the supervisor is never blocked.

---

## Restart behaviour

On every `Start`, a fresh `*grpc.Server` is created, all registered
`RegisterFunc`s and `AddOption` options are re-applied in registration order,
and the health service is re-initialised. This means restarts are clean — no
state from the previous run leaks into the new one.

---

## Multiple servers

```go
internal := sc.New(sc.Config{Host: "127.0.0.1", Port: 9090},
    sc.WithName("grpc-internal"))
public := sc.New(sc.Config{Host: "0.0.0.0", Port: 9091},
    sc.WithName("grpc-public"))

internal.Register(func(s *grpclib.Server) {
    pb.RegisterAdminServiceServer(s, adminImpl)
})
public.Register(func(s *grpclib.Server) {
    pb.RegisterUserServiceServer(s, userImpl)
})

sup.Add(internal, samsara.WithTier(samsara.TierCritical))
sup.Add(public, samsara.WithTier(samsara.TierCritical))
```
