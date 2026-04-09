# grpcclient

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/grpcclient.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/grpcclient)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/grpcclient)](https://goreportcard.com/report/github.com/sunkek/samsara-components/grpcclient)

A [samsara](https://github.com/sunkek/samsara)-compatible gRPC client component
backed by [google.golang.org/grpc](https://github.com/grpc/grpc-go).

```
go get github.com/sunkek/samsara-components/grpcclient
```

---

## Usage

### Register with a supervisor

```go
import (
    sc "github.com/sunkek/samsara-components/grpcclient"
    grpclib "google.golang.org/grpc"
)

client := sc.New(sc.Config{
    Target: "user-service:9090",
})
sup.Add(client,
    samsara.WithTier(samsara.TierCritical),
    samsara.WithDependencies("postgres"), // connect after DB is ready
)
```

### Pass the connection to generated stubs

Call `Conn()` to get the underlying `*grpc.ClientConn` and pass it to your
generated stub constructors. Do this in your wiring code in `main.go`, after
`sup.Add` — the connection object exists immediately even before `Start` runs:

```go
userClient := pb.NewUserServiceClient(client.Conn())
orderClient := pb.NewOrderServiceClient(client.Conn())

// Pass stubs to your domain adapters:
userAdapter := user.New(userClient, db)
orderAdapter := order.New(orderClient, mq)
```

`Conn()` returns `nil` before `Start` is called. Because samsara starts
components in dependency order, any component that declares
`WithDependencies("grpc-client")` is guaranteed to see a non-nil `Conn()`.

### Typical wiring pattern

```go
// In main.go:
userServiceClient := sc.New(sc.Config{Target: cfg.UserServiceAddr},
    sc.WithName("user-service-client"),
)
sup.Add(userServiceClient, samsara.WithTier(samsara.TierCritical))

// Adapters receive the stub, not the component:
userAdapter := user.NewAdapter(pb.NewUserServiceClient(userServiceClient.Conn()))
```

---

## Interceptors

Use `AddOption` to inject unary and stream interceptors — for auth, logging,
tracing, metrics, and so on. Must be called before `Start`.

```go
// Single interceptor:
client.AddOption(grpclib.WithChainUnaryInterceptor(authInterceptor))

// Multiple interceptors execute left to right:
client.AddOption(grpclib.WithChainUnaryInterceptor(
    authInterceptor,
    loggingInterceptor,
    tracingInterceptor,
))
client.AddOption(grpclib.WithChainStreamInterceptor(
    authStreamInterceptor,
))
```

A unary client interceptor has this signature:

```go
func myInterceptor(
    ctx context.Context,
    method string,
    req, reply any,
    cc *grpclib.ClientConn,
    invoker grpclib.UnaryInvoker,
    opts ...grpclib.CallOption,
) error {
    // before RPC
    err := invoker(ctx, method, req, reply, cc, opts...)
    // after RPC
    return err
}
```

---

## Configuration

```go
sc.Config{
    Target string        // required — host:port, dns:///host:port, etc.

    ConnectTimeout time.Duration // default: 10s — deadline to reach READY state

    MaxRecvMsgSizeMB int  // default: 4  — max inbound message size
    MaxSendMsgSizeMB int  // default: 4  — max outbound message size

    // Client keepalive (how the client pings idle servers)
    KeepaliveTime                time.Duration // default: 30 s
    KeepaliveTimeout             time.Duration // default: 10 s
    KeepalivePermitWithoutStream *bool         // default: true
}
```

### Options

```go
sc.WithLogger(slog.Default())           // attach a structured logger
sc.WithName("user-service-client")      // override component name
```

---

## Connection targets

`Target` accepts any string supported by gRPC's name resolver:

```go
// Plain address (most common):
Target: "localhost:9090"
Target: "user-service:9090"

// DNS resolver with multiple backends (client-side load balancing):
Target: "dns:///user-service.internal:9090"

// Custom resolver (register via resolver.Register before Start):
Target: "myscheme:///user-service"
```

---

## Health checking

`*Component` implements `samsara.HealthChecker`. The supervisor polls
`Health(ctx)` every health interval. Health returns an error if the connection
is not in `READY` or `IDLE` state.

`IDLE` is considered healthy because gRPC clients re-enter `IDLE` after a
period of inactivity (by design) and reconnect automatically on the next RPC.
`CONNECTING` and `TRANSIENT_FAILURE` are considered unhealthy — they indicate
the client is actively failing to reach the server.

---

## Graceful shutdown

On `Stop`, the component calls `conn.Close()`, which:
1. Cancels all in-flight RPCs with a `codes.Canceled` error
2. Closes all underlying HTTP/2 connections
3. Releases all resources held by the connection pool

If `conn.Close()` does not return before the context deadline, Stop logs an
error and returns — it does not hang the supervisor.

---

## Restart behaviour

On every `Start`, a fresh `*grpc.ClientConn` is created and the component
waits for it to reach `READY` before calling `ready()`. The previous
connection (if any) is already closed by `Stop`. This means restarts are
clean and the caller always gets a live connection from `Conn()`.

---

## Multiple clients

Register one component per backend service:

```go
userClient := sc.New(sc.Config{Target: cfg.UserServiceAddr},
    sc.WithName("user-service-client"))
orderClient := sc.New(sc.Config{Target: cfg.OrderServiceAddr},
    sc.WithName("order-service-client"))

sup.Add(userClient, samsara.WithTier(samsara.TierCritical))
sup.Add(orderClient, samsara.WithTier(samsara.TierCritical))
```

---

## Why not just use `grpc.NewClient` directly?

A raw `*grpc.ClientConn` works fine for simple services. The component earns
its place when you need:

- **Ordered startup** — the client connects only after its dependencies (config
  loader, secrets manager, etc.) have finished their own `Start`.
- **Ordered shutdown** — the connection drains before the services that depend
  on it are torn down, preventing in-flight RPCs from hitting a closed pool.
- **Supervisor visibility** — connectivity state is surfaced through
  `/readyz` and health hooks, the same as every other component.
- **Restart policy** — if the remote service is temporarily unreachable at
  startup, samsara can retry with exponential backoff automatically.
- **Consistent observability** — the same structured logger, the same hook
  events, the same restart-policy knobs as PostgreSQL, RabbitMQ, etc.
