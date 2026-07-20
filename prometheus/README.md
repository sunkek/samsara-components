# prometheus

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/prometheus.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/prometheus)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/prometheus)](https://goreportcard.com/report/github.com/sunkek/samsara-components/prometheus)

A [samsara](https://github.com/sunkek/samsara)-compatible Prometheus metrics
component backed by
[client_golang](https://github.com/prometheus/client_golang).

```
go get github.com/sunkek/samsara-components/prometheus
```

---

## What it provides

- **Component** — a supervised HTTP server exposing a Prometheus registry in
  exposition format (default `:2112/metrics`).
- **Observer** — a `samsara.MetricsObserver` implementation (structural, no
  samsara import) bridging supervisor telemetry into that registry.

## Usage

### Register with a supervisor

```go
m := prometheus.New(prometheus.Config{Port: 2112})

sup := samsara.NewSupervisor(
    samsara.WithMetricsObserver(m.Observer()),
)
sup.Add(m, samsara.WithTier(samsara.TierAuxiliary))
```

### Application metrics

Register collectors on the component's registry:

```go
requests := prom.NewCounterVec(prom.CounterOpts{
    Name: "http_requests_total",
    Help: "Total HTTP requests.",
}, []string{"method", "route", "status"})

m.Registry().MustRegister(requests)
```

### Embed into an existing server

Skip the component and mount the handler yourself:

```go
app.Get("/metrics", adaptor.HTTPHandler(m.Handler()))
```

## Supervisor telemetry series

| Series | Type | Labels |
|---|---|---|
| `samsara_component_up` | gauge | `component` |
| `samsara_component_starts_total` | counter | `component` |
| `samsara_component_restarts_total` | counter | `component` |
| `samsara_component_stop_errors_total` | counter | `component` |
| `samsara_component_health_check_duration_seconds` | histogram | `component` |
| `samsara_component_health_check_failures_total` | counter | `component` |

## Config

| Field | Default | Description |
|---|---|---|
| `Host` | `""` (all interfaces) | Bind interface |
| `Port` | `2112` | Listen port |
| `Path` | `/metrics` | Exposition path |
| `ReadTimeout` | `5s` | Request read deadline |
| `WriteTimeout` | `30s` | Response write deadline (bounds scrape size) |
| `DisableRuntimeCollectors` | `false` | Skip Go runtime / process collectors on the default registry |

## Options

- `WithLogger(l)` — structured logger (`*slog.Logger` satisfies it directly)
- `WithName(name)` — override the component name
- `WithRegistry(reg)` — serve a caller-owned registry (caller registers runtime collectors)

## Health

`Health` returns `ErrNotReady` until the server is listening, `nil` while
serving.
