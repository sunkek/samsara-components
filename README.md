# samsara-components

[![CI](https://github.com/sunkek/samsara-components/actions/workflows/ci.yml/badge.svg)](https://github.com/sunkek/samsara-components/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/postgresql.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/postgresql)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Production-ready infrastructure components for [samsara](https://github.com/sunkek/samsara) — the explicit lifecycle runtime for Go services.

Each component is an independent Go module. Import only what you need.

---

## Components

| Module | Import path | Description |
|--------|-------------|-------------|
| [`postgresql`](./postgresql) | `github.com/sunkek/samsara-components/postgresql` | PostgreSQL connection pool via pgx/v5 |
| `redis` _(planned)_ | `github.com/sunkek/samsara-components/redis` | Redis client |
| `rabbitmq` _(planned)_ | `github.com/sunkek/samsara-components/rabbitmq` | RabbitMQ consumer/publisher |
| `s3` _(planned)_ | `github.com/sunkek/samsara-components/s3` | S3-compatible object storage |
| `fiber` _(planned)_ | `github.com/sunkek/samsara-components/fiber` | Fiber HTTP server |

---

## Quick start

```go
import (
    "github.com/sunkek/samsara"
    "github.com/sunkek/samsara-components/postgresql"
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
| `make infra-up` | Start Postgres, Redis, RabbitMQ via Docker Compose |
| `make test-integration` | Start infra, run integration tests, stop infra |
| `make tidy` | `go mod tidy` across all modules |

---

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first.

Every PR must pass `make check` and `make test-all`.

## License

MIT — see [LICENSE](LICENSE).