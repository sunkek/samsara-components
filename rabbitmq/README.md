# rabbitmq

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/rabbitmq.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/rabbitmq)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/rabbitmq)](https://goreportcard.com/report/github.com/sunkek/samsara-components/rabbitmq)

A [samsara](https://github.com/sunkek/samsara)-compatible RabbitMQ component
backed by [amqp091-go](https://github.com/rabbitmq/amqp091-go).

```
go get github.com/sunkek/samsara-components/rabbitmq
```

---

## Usage

### Register with a supervisor

```go
mq := rabbitmq.New(rabbitmq.Config{
    Host:  "localhost",
    Port:  5672,
    VHost: "myapp",
    User:  "myuser",
    Pass:  "secret",
})
sup.Add(mq,
    samsara.WithTier(samsara.TierCritical),
    samsara.WithRestartPolicy(samsara.ExponentialBackoff(5, time.Second)),
)
```

Or supply a full URI:

```go
mq := rabbitmq.New(rabbitmq.Config{
    URI: "amqp://user:pass@host:5672/vhost",
})
```

### Declare exchanges and subscribe

Exchanges and subscriptions can be registered at any time — before or after app.Run(). 
If the component is already running, DeclareExchange and Subscribe take effect immediately on the live channel. 
If it hasn't started yet, they are applied on the next Start.
On every restart the component re-declares all registered exchanges and re-binds all subscriptions automatically, in registration order.

```go
mq.DeclareExchange("events", rabbitmq.ExchangeTopic, true)

mq.Subscribe("events", "user.created", func(d amqp.Delivery) error {
    // handle message — return nil to ack, non-nil to nack with requeue
    return json.Unmarshal(d.Body, &event)
})

// Use SubscribeWithKey for wildcard routing keys on topic exchanges:
mq.SubscribeWithKey("events", "user.queue", "user.#", handleUserEvent)
```

#### Dead-letter queues and redelivery caps

`SubscribeWithOptions` declares the work queue with custom `x-arguments`, so the
broker owns dead-lettering and the redelivery limit. Pair it with a handler that
returns `ErrDropToDLX` to nack **without** requeue, firing the queue's DLX:

```go
mq.SubscribeWithOptions("events", "work.queue", "work.#", handler,
    rabbitmq.SubscribeOptions{
        QueueType: rabbitmq.QueueTypeQuorum, // required for x-delivery-limit
        QueueArgs: amqp.Table{
            "x-dead-letter-exchange": "events.dlx",
            "x-delivery-limit":       int32(5), // broker-enforced cap
        },
    },
)

func handler(d amqp.Delivery) error {
    if permanentFailure {
        return fmt.Errorf("bad payload: %w", rabbitmq.ErrDropToDLX) // nack, no requeue → DLX
    }
    return transientErr // nack with requeue → retry in place (quorum cap applies)
}
```

#### Component-managed retries with backoff (delayed retry + terminal DLQ)

For delayed retries with backoff — instead of the broker's immediate
requeue-in-place — set `SubscribeOptions.Retry`. The component declares two
extra queues alongside the work queue:

- `<queue>.retry` — a delay queue with no consumers; retried messages sit here
  with a per-message TTL and dead-letter back into the work queue when it
  expires;
- `<queue>.dlq` — the terminal dead-letter queue, populated once `MaxRetries`
  is exhausted or immediately when the handler returns `ErrDropToDLX`.

```go
mq.SubscribeWithOptions("events", "work.queue", "work.#", handler,
    rabbitmq.SubscribeOptions{
        Retry: &rabbitmq.RetryPolicy{
            MaxRetries:        5,                      // default 3; negative → straight to DLQ
            Backoff:           500 * time.Millisecond, // default 1s
            BackoffMultiplier: 2,                      // default 2 (exponential)
            MaxBackoff:        time.Minute,            // default 5m
            // RetryQueue / DLQ override the default names.
        },
    },
)
```

The attempt counter travels in the `x-retry-count` header
(`rabbitmq.RetryHeader`). On each failure the component republishes the
message (with headers, correlation ID, and body preserved) and acks the
original, so the retry pipeline also works on classic queues and does not
depend on `x-delivery-limit`. If the republish itself fails, the message is
nacked with requeue so nothing is lost.

### Publish

```go
err := mq.Publish(ctx, "events", "user.created",
    rabbitmq.ContentTypeJSON,
    body,
)

// With AMQP message type field (useful for event-driven routing):
err := mq.PublishWithType(ctx, "events", "user.created",
    rabbitmq.ContentTypeJSON, "UserCreated",
    body,
)

// With custom AMQP headers (e.g. an attempt counter on a republished message):
err := mq.PublishWithHeaders(ctx, "events", "user.created",
    rabbitmq.ContentTypeJSON,
    amqp.Table{"x-attempt": int32(2)},
    body,
)
```

`Publish` respects the caller's context and uses the configured
`PublishTimeout` as a per-call deadline. It does not retry internally —
retry strategy (exponential backoff, dead-letter, drop) is a domain concern.

---

## Configuration

```go
rabbitmq.Config{
    // Individual fields (all have sensible defaults)
    Host  string        // default: "localhost"
    Port  int           // default: 5672
    VHost string        // default: "/"
    User  string        // default: "guest"
    Pass  string        // special characters are safely percent-encoded

    // URI override — takes precedence when non-empty
    URI string

    // Timeouts
    ConnectTimeout time.Duration // default: 10s — startup dial deadline
    PublishTimeout time.Duration // default: 5s  — per-publish deadline

    OnOperation func(op string, d time.Duration, err error) // default: nil — see Metrics
}
```

### Options

```go
rabbitmq.WithLogger(slog.Default())     // attach a structured logger
rabbitmq.WithName("events-broker")      // override component name
```

---

## API reference

### Exchange kinds

| Constant | AMQP type |
|----------|-----------|
| `ExchangeDirect` | `"direct"` |
| `ExchangeTopic` | `"topic"` |
| `ExchangeFanout` | `"fanout"` |
| `ExchangeHeaders` | `"headers"` |

### Content types

| Constant | Value |
|----------|-------|
| `ContentTypeJSON` | `application/json` |
| `ContentTypeJSONUTF8` | `application/json; charset=utf-8` |
| `ContentTypeText` | `text/plain` |
| `ContentTypeBytes` | `application/octet-stream` |

### Methods

| Method | Description |
|--------|-------------|
| `DeclareExchange(name, kind, durable)` | Register an exchange; re-declared on restart |
| `Subscribe(exchange, queue, handler)` | Bind queue with routing key = queue name |
| `SubscribeWithKey(exchange, queue, key, handler)` | Bind with explicit routing key |
| `SubscribeWithOptions(exchange, queue, key, handler, opts)` | Bind, declaring the queue with `QueueArgs`/`QueueType` (DLX, `x-delivery-limit`, quorum); set `opts.Retry` for component-managed delayed retries + terminal DLQ |
| `Publish(ctx, exchange, routingKey, contentType, body)` | Publish a message |
| `PublishWithType(ctx, exchange, routingKey, contentType, messageType, body)` | Publish with AMQP type field |
| `PublishWithHeaders(ctx, exchange, routingKey, contentType, headers, body)` | Publish with custom AMQP headers |

### Message handler contract

```go
func handler(d amqp.Delivery) error {
    // Return nil           → message is acked (removed from queue)
    // Return ErrDropToDLX   → nacked with requeue=false (dead-lettered via queue DLX)
    // Return any other err  → nacked with requeue=true (retried in place)
}
```

Messages are published as `DeliveryMode: Persistent` by default.

---

## Health checking

`*Component` implements `samsara.HealthChecker`. The supervisor polls
`Health(ctx)` every health interval. Health fails if:

- any consumer goroutine died while the component was running (the broker
  cancelled the consumer or closed its delivery channel) — the restart
  re-binds all subscriptions and clears the condition; or
- the AMQP connection or channel is closed — typically indicating a
  broker-side disconnect.

---

## Restart behaviour

On restart, `Start` re-declares all exchanges and re-binds all subscriptions
in the order they were registered. Consumer goroutines from the previous run
exit cleanly because they select on the supervisor's component context, which
is cancelled before the restart attempt begins.

Subscriptions registered after `Start` (via `Subscribe` or `SubscribeWithKey`)
are bound immediately on the live channel and will also be re-bound on the next
restart.

---

## Multiple brokers

```go
primary := rabbitmq.New(cfg.Primary, rabbitmq.WithName("rabbitmq-primary"))
failover := rabbitmq.New(cfg.Failover, rabbitmq.WithName("rabbitmq-failover"))

sup.Add(primary, samsara.WithTier(samsara.TierCritical))
sup.Add(failover, samsara.WithTier(samsara.TierSignificant))
```

---

## Escape hatch

Two accessors, for AMQP work the `Publisher` interface and `Subscribe` do not
cover:

```go
conn := mq.Conn()    // *amqp.Connection — nil before Start, after Stop, during a reconnect
ch := mq.Channel()   // *amqp.Channel — the component's own, shared channel
```

Prefer `Conn` and open a channel of your own for anything long-lived: an
`amqp.Channel` is not safe for concurrent use, and the component's channel is
shared with every publish and every subscription. Closing that channel disables
the component until its next restart.

```go
own, err := mq.Conn().Channel()
defer own.Close()
_, err = own.QueueInspect("orders")
```

Depend on the component via `samsara.WithDependencies` if you need the
connection at startup, so `Start` has already run. Work done through either
accessor bypasses the component's logging, retry topology and metrics.
See [ADR-0005](../docs/adr/0005-driver-escape-hatch-accessors.md).

---

## Sentinel errors

```go
errors.Is(err, rabbitmq.ErrNotReady) // no live channel; the publish was not attempted
```

---

## Metrics

`Config.OnOperation` is called once per completed `Publisher` operation with a fixed
operation name, how long the call took, and the error it returned. It defaults to
nil, which disables reporting entirely.

```go
c := rabbitmq.New(rabbitmq.Config{
    OnOperation: func(op string, d time.Duration, err error) {
        // op is one of: rabbitmq.publish, rabbitmq.publish_with_type, rabbitmq.publish_with_headers
        metrics.Observe(op, d, err)
    },
})
```

`ErrNotReady` is not an operation failure: it means there was no live handle and
the call was never attempted. Those are reported with a **zero duration**, so
they never enter the latency distribution. Classify accordingly before counting
error rates.

Coverage is publishes only — deliveries consumed by a subscription are not
measured.

See [ADR-0006](../docs/adr/0006-metrics-behind-the-narrow-interface.md).
