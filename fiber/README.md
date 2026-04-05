# fiber

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/fiber.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/fiber)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/fiber)](https://goreportcard.com/report/github.com/sunkek/samsara-components/fiber)

A [samsara](https://github.com/sunkek/samsara)-compatible HTTP server component
backed by [Fiber v3](https://github.com/gofiber/fiber).

```
go get github.com/sunkek/samsara-components/fiber
```

---

## Usage

### Register with a supervisor

```go
import (
    "github.com/sunkek/samsara-components/fiber"
    gf "github.com/gofiber/fiber/v3"
)

api := fiber.New(fiber.Config{
    Host:       "0.0.0.0",
    Port:       8080,
    PathPrefix: "/api/v1",
})
sup.Add(api, samsara.WithTier(samsara.TierCritical))
```

### Register domain routes

Pass a `RegisterFunc` to `Register`. It receives the root `gf.Router` (scoped
to `PathPrefix`) and registers routes and sub-groups on it:

```go
srv.Register(func(r gf.Router) {
    r.Get("/users",   handleGetUsers)
    r.Post("/users",  handleCreateUser)
    r.Delete("/users/:id", handleDeleteUser)

    // Sub-groups work naturally:
    admin := r.Group("/admin", adminAuthMiddleware)
    admin.Get("/stats", handleStats)
})
```

Domain adapters should call `srv.Register` in their constructor or wiring
function, before `app.Run()`:

```go
// In your adapter:
func (a *UserAdapter) RegisterRoutes(srv *sc.Component) {
    srv.Register(func(r gf.Router) {
        r.Get("/users",  a.handleGetUsers)
        r.Post("/users", a.handleCreateUser)
    })
}

// In main.go:
userAdapter := user.New(db)
userAdapter.RegisterRoutes(srv)
```

`Register` is also safe to call after `Start` — routes are applied immediately
on the live app and will be re-registered on the next restart.

### Add global middleware

Use `Use` to inject middleware that wraps all domain routes (auth, tracing, etc.).
Must be called before `Start` to take effect on the current run.

```go
srv.Use(authMiddleware, tracingMiddleware)
```

---

## Built-in middleware stack

Applied automatically in this order:

| Middleware | Notes |
|------------|-------|
| Recover | Catches panics; stack trace enabled |
| CORS | Configured via `CORSAllowOrigins/Methods/Headers` |
| Security headers | HSTS, X-Frame-Options, CORP/COEP (disable with `EnableSecurityHeaders: &false`) |
| Compress | `BestSpeed` level |
| *Caller-supplied* `Use(...)` | Auth, tracing, etc. |
| Request logger | Structured JSON; disable with `LoggerFormat: "-"` |
| *Domain routes* | Everything registered via `Register(...)` |

---

## Configuration

```go
sc.Config{
    Host       string        // default: "0.0.0.0"
    Port       int           // default: 8080
    PathPrefix string        // default: "/api/v1"
    BodyLimitMB int          // default: 4

    CORSAllowOrigins []string // default: ["*"]
    CORSAllowMethods []string // default: ["GET","POST","PUT","PATCH","DELETE","OPTIONS"]
    CORSAllowHeaders []string // default: ["*"]

    ReadTimeout  time.Duration // default: Fiber default
    WriteTimeout time.Duration // default: Fiber default
    IdleTimeout  time.Duration // default: Fiber default

    ErrorHandler  gf.ErrorHandler // default: DefaultErrorHandler
    LoggerFormat  string          // default: structured JSON; "-" disables
    EnableSecurityHeaders *bool   // default: true
}
```

### Options

```go
sc.WithLogger(slog.Default())   // attach a structured logger
sc.WithName("api-server")       // override component name
sc.WithSwagger(sc.SwaggerConfig{
    JSONPath: "./docs/swagger.json",
})                              // enable Swagger UI at /api/docs
```

---

## Error handling

`DefaultErrorHandler` maps errors to HTTP status codes:

| Error type | Status |
|------------|--------|
| `*gf.Error` | Error's own `.Code` |
| Implements `HTTPStatuser` | `.StatusCode()` |
| Anything else | 500 |

To integrate your own error library, implement `HTTPStatuser`:

```go
type NotFoundError struct{ Resource string }
func (e *NotFoundError) Error() string   { return e.Resource + " not found" }
func (e *NotFoundError) StatusCode() int { return http.StatusNotFound }
```

Or supply a fully custom `ErrorHandler`:

```go
cfg.ErrorHandler = func(c gf.Ctx, err error) error {
    var myErr *myapp.DomainError
    if errors.As(err, &myErr) {
        return c.Status(myErr.HTTPStatus()).JSON(sc.ErrorResponse{Error: myErr.Error()})
    }
    return sc.DefaultErrorHandler(c, err) // fallback
}
```

---

## Swagger UI

```go
sc.WithSwagger(sc.SwaggerConfig{
    JSONPath: "./docs/swagger.json",
    UIPath:   "/docs",           // default; UI available at /api/docs
})(srv)
```

The spec is served at `/api/docs/swagger.json`; the UI at `/api/docs`.

Generate the spec with [swaggo/swag](https://github.com/swaggo/swag):

```bash
swag init -g main.go -o ./docs
```

---

## Built-in endpoints

| Endpoint | Status | Purpose |
|----------|--------|---------|
| `GET {PathPrefix}/health` | 204 | Kubernetes/Docker health probe |

The `/health` endpoint is registered before the logger middleware, so probe
traffic does not appear in access logs.

---

## Helper utilities

```go
// Real client IP, honouring X-Forwarded-For:
ip := sc.RealIP(c)

// Build a middleware skipper that excludes specific routes:
skipper := sc.ExcludeRoutes(
    sc.Route{Method: "GET", Path: "/api/health"},
    sc.Route{Method: "GET", Path: "/api/metrics"},
)
srv.Use(func(c gf.Ctx) error {
    if skipper(c) { return c.Next() }
    return authMiddleware(c)
})
```

---

## Health checking

`*Component` implements `samsara.HealthChecker`. The supervisor polls
`Health(ctx)` every health interval. Health fails if:
- The server is not listening (not yet started, or shutting down).
- The built-in `/health` endpoint does not return 204.

---

## Restart behaviour

On every `Start`, the full middleware stack and all registered `RegisterFunc`s
are re-applied in registration order. This means restarts are clean — no state
leaks from the previous run.
