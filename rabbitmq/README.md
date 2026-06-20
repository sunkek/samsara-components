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
| `SubscribeWithOptions(exchange, queue, key, handler, opts)` | Bind, declaring the queue with `QueueArgs`/`QueueType` (DLX, `x-delivery-limit`, quorum) |
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
`Health(ctx)` every health interval. Health fails if either the AMQP
connection or channel is closed — typically indicating a broker-side
disconnect, which triggers a restart.

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
