# Changelog

All notable changes to samsara-components are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Each component module is versioned independently; this file tracks changes
across all of them.

---

## [Unreleased]

---

## fiber/v0.1.0 — 2026-04-05

Initial release of the Fiber HTTP server component.

### Added
- `Component` — samsara-compatible Fiber v3 HTTP server
- `Config` — structured config: host, port, path prefix, body limit, CORS, timeouts, error handler, logger format, security headers
- `WithLogger`, `WithName`, `WithSwagger` options
- `Register(RegisterFunc)` — callback-based route registration; safe before and after `Start`
- `Use(...)` — global middleware injection before domain routes
- `DefaultErrorHandler` with `HTTPStatuser` interface for library-agnostic error mapping
- `ErrorResponse` JSON shape
- `RealIP`, `ExcludeRoutes`, `Route`, `SkipperFunc` helper utilities
- Built-in middleware stack: recover, CORS, security headers, compress, request logger
- Built-in `GET {PathPrefix}/health` endpoint (204, excluded from access logs)
- Compile-time samsara interface assertion
- Unit tests (no server binding required)
- Integration tests (`//go:build integration`) using ephemeral ports

---

## rabbitmq/v0.1.0 — 2026-04-05

Initial release of the RabbitMQ component.

### Added
- `Component` — samsara-compatible AMQP component backed by amqp091-go
- `Config` — structured config with individual fields and `URI` override; credentials percent-encoded
- `WithLogger`, `WithName` options
- `DeclareExchange(name, kind, durable)` — registered and re-declared on restart
- `Subscribe(exchange, queue, handler)` — queue binding with routing key = queue name
- `SubscribeWithKey(exchange, queue, routingKey, handler)` — explicit routing key for topic patterns
- `Publish(ctx, exchange, routingKey, contentType, body)` — context-aware publish
- `PublishWithType(...)` — publish with AMQP message type field
- `ExchangeKind` constants: Direct, Topic, Fanout, Headers
- `ContentType` constants: JSON, JSON+UTF8, Text, Bytes
- Context-aware dial: races `amqp.DialConfig` against `ConnectTimeout` and `ctx`
- Consumer goroutines tied to component context; exit cleanly on Stop/restart
- Compile-time samsara interface assertion
- Unit tests (no broker required)
- Integration tests (`//go:build integration`) against a live RabbitMQ instance

---

## postgresql/v0.1.0 — 2026-04-04

Initial release of the PostgreSQL component.

### Added
- `Component` — samsara-compatible lifecycle wrapper around a `pgxpool.Pool`
- `Config` — structured config with individual fields and `URI` override
- `WithLogger`, `WithName` options
- `DB` interface — `Select`, `Get`, `Exec`, `BeginTx`, `CommitTx`
- `TxFinaliser` interface for stub-based transaction testing
- `ErrNoRows` sentinel (aliases `pgx.ErrNoRows`)
- Compile-time samsara interface assertion
- Unit tests (race detector, count=3, no database required)
- Integration tests (`//go:build integration`) against a live Postgres instance
- `docker-compose.yml` with ephemeral Postgres, Redis, RabbitMQ
- `Makefile` with `check`, `test-race`, `coverage`, `test-integration`, `tidy`
- GitHub Actions CI: unit + static analysis + integration jobs
