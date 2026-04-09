# samsara-components

[![CI](https://github.com/sunkek/samsara-components/actions/workflows/ci.yml/badge.svg)](https://github.com/sunkek/samsara-components/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Production-ready infrastructure components for [samsara](https://github.com/sunkek/samsara) — the explicit lifecycle runtime for Go services.

Each component is an independent Go module. Import only what you need.

---

## Components

| Module | Import path | Description |
|--------|-------------|-------------|
| [`fiber`](./fiber) | `github.com/sunkek/samsara-components/fiber` | Fiber HTTP server |
| [`grpc`](./grpc) | `github.com/sunkek/samsara-components/grpc` | gRPC server with health, reflection, and interceptor support |
| [`grpcclient`](./grpcclient) | `github.com/sunkek/samsara-components/grpcclient` | gRPC client with lifecycle management and interceptor support |
| [`postgresql`](./postgresql) | `github.com/sunkek/samsara-components/postgresql` | PostgreSQL connection pool via pgx/v5 |
| [`rabbitmq`](./rabbitmq) | `github.com/sunkek/samsara-components/rabbitmq` | RabbitMQ consumer/publisher |
| [`redis`](./redis) | `github.com/sunkek/samsara-components/redis` | Redis client |
| [`s3`](./s3) | `github.com/sunkek/samsara-components/s3` | S3-compatible object storage |

---

## Quick start

```go
import (
    "github.com/sunkek/samsara"
    "github.com/sunkek/samsara-components/fiber"
    "github.com/sunkek/samsara-components/grpc"
    "github.com/sunkek/samsara-components/grpcclient"
    "github.com/sunkek/samsara-components/postgresql"
    "github.com/sunkek/samsara-components/rabbitmq"
    grpclib "google.golang.org/grpc"
)

func main() {
    sup := samsara.NewSupervisor()

    db := postgresql.New(postgresql.Config{
        Host: "localhost",
        Port: 5432,
        Name: "mydb",
        User: "myuser",
        Pass: "secret",
    })
    sup.Add(db, samsara.WithTier(samsara.TierCritical))

    mq := rabbitmq.New(rabbitmq.Config{
        Host: "localhost",
        Port: 5672,
        VHost: "vhost",
        User: "myuser",
        Pass: "secret",
    })
    sup.Add(mq, samsara.WithTier(samsara.TierCritical))

    cache := redis.New(redis.Config{
        Host: "localhost",
        Port: 6379,
    })
    sup.Add(cache,
        samsara.WithTier(samsara.TierCritical),
        samsara.WithRestartPolicy(samsara.ExponentialBackoff(5, time.Second)),
    )

    store := s3.New(s3.Config{
        Endpoint: "https://s3.us-east-1.amazonaws.com",
        Region:   "us-east-1",
        KeyID:    os.Getenv("S3_KEY_ID"),
        Secret:   os.Getenv("S3_SECRET"),
    })
    sup.Add(store,
        samsara.WithTier(samsara.TierSignificant),
        samsara.WithRestartPolicy(samsara.AlwaysRestart(5*time.Second)),
    )

    rest := fiber.New(fiber.Config{
        Host:       "0.0.0.0",
        Port:       8080,
        PathPrefix: "/api/v1",
    })
    sup.Add(rest, samsara.WithTier(samsara.TierCritical))

    rpc := grpc.New(grpc.Config{
        Host: "0.0.0.0",
        Port: 9090,
    })
    rpc.Register(func(s *grpclib.Server) {
        pb.RegisterMyServiceServer(s, &myServiceImpl{db: db})
    })
    sup.Add(rpc, samsara.WithTier(samsara.TierCritical))

    upstream := grpcclient.New(grpcclient.Config{
        Target: "other-service:9090",
    }, grpcclient.WithName("other-service-client"))
    sup.Add(upstream,
        samsara.WithTier(samsara.TierCritical),
        samsara.WithDependencies("postgres"),
    )
    otherClient := pb.NewOtherServiceClient(upstream.Conn())
    _ = otherClient // pass to adapters that need it

    app := samsara.NewApplication(samsara.WithSupervisor(sup))
    if err := app.Run(); err != nil {
        log.Fatal(err)
    }
}
```

See each component's README for full usage documentation.

---

## Development

This repository uses a [Go workspace](https://go.dev/ref/mod#workspaces) so all
modules can be developed together without publishing.

```
git clone https://github.com/sunkek/samsara-components
cd samsara-components
make check          # vet + lint + unit tests with race detector
make test-all       # unit + integration (requires Docker)
```

### Prerequisites

- Go 1.25+
- Docker with Compose v2 (for integration tests only)
- [`staticcheck`](https://staticcheck.dev) — installed automatically by `make lint`

### Useful targets

| Target | Description |
|--------|-------------|
| `make check` | Vet + lint + unit tests — run before pushing |
| `make test-race` | Unit tests with race detector, count=3 |
| `make coverage` | Unit tests with per-module coverage summary |
| `make infra-up` | Start Postgres, Redis, RabbitMQ, SeaweedFS via Docker Compose (not needed for grpc/grpcclient) |
| `make test-integration` | Start infra, run integration tests, stop infra |
| `make tidy` | `go mod tidy` across all modules |

---

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first.

Every PR must pass `make check` and `make test-all`.

## License

MIT — see [LICENSE](LICENSE).
