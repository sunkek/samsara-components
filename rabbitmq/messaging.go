package rabbitmq

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ContentType is the MIME type of a published message body.
type ContentType string

const (
	ContentTypeJSON     ContentType = "application/json"
	ContentTypeJSONUTF8 ContentType = "application/json; charset=utf-8"
	ContentTypeText     ContentType = "text/plain"
	ContentTypeBytes    ContentType = "application/octet-stream"
)

// subscription describes a queue binding and its message handler.
// The consumer goroutine's lifetime is tied to the component context passed
// into Start, not to an arbitrary caller context.
type subscription struct {
	routingKey string
	exchange   string
	queue      string
	handler    func(amqp.Delivery) error
}

// DeclareExchange registers an exchange to be declared on every Start.
// It is safe to call before Start; the declaration is applied (and re-applied
// on restart) when the component connects.
//
// durable controls whether the exchange survives broker restarts.
// For most production use cases, durable should be true.
//
// Returns an error if Start has already been called and the live channel
// rejects the declaration (e.g. parameter mismatch with an existing exchange).
func (c *Component) DeclareExchange(name string, kind ExchangeKind, durable bool) error {
	c.exchMu.Lock()
	c.exchanges = append(c.exchanges, exchangeDecl{name: name, kind: kind, durable: durable})
	c.exchMu.Unlock()

	// If the component is already running, declare immediately on the live channel.
	c.mu.RLock()
	ch := c.ch
	c.mu.RUnlock()

	if ch == nil || ch.IsClosed() {
		return nil // not running yet — will be declared on next Start
	}
	if err := ch.ExchangeDeclare(name, string(kind), durable, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare exchange %q: %w", name, err)
	}
	return nil
}

// Subscribe registers a queue binding and message handler. It is safe to call
// before Start; on every Start the component re-binds all subscriptions.
//
// If the component is already running, the subscription is bound immediately
// on the live channel.
//
// The routing key equals the queue name. For topic exchanges, use explicit
// routing keys via [SubscribeWithKey].
func (c *Component) Subscribe(exchange, queue string, handler func(amqp.Delivery) error) error {
	return c.SubscribeWithKey(exchange, queue, queue, handler)
}

// SubscribeWithKey is like [Subscribe] but uses an explicit routing key,
// allowing patterns like "user.#" on topic exchanges.
func (c *Component) SubscribeWithKey(exchange, queue, routingKey string, handler func(amqp.Delivery) error) error {
	sub := subscription{exchange: exchange, queue: queue, routingKey: routingKey, handler: handler}

	c.subsMu.Lock()
	c.subs = append(c.subs, sub)
	c.subsMu.Unlock()

	// If the component is already running, bind immediately using the current
	// run's context so the consumer goroutine exits when Stop is called.
	// Holding the read lock only long enough to read both ch and runCtx keeps
	// the critical section short.
	c.mu.RLock()
	ch := c.ch
	runCtx := c.runCtx
	c.mu.RUnlock()

	if ch == nil || ch.IsClosed() {
		return nil // not running yet — will be bound on next Start
	}
	if runCtx == nil {
		// Component was stopped between the IsClosed check and here; the next
		// Start will re-bind this subscription from the slice.
		return nil
	}
	return c.bindAndConsume(runCtx, ch, sub)
}

// Publish sends a message to the given exchange with the given routing key.
// It respects ctx for cancellation and uses the configured PublishTimeout
// as a per-attempt deadline.
//
// Publish does not retry internally. If you need retry logic, wrap this call
// in your own retry loop — the appropriate strategy (retry, dead-letter, drop)
// is a domain concern, not an infrastructure one.
func (c *Component) Publish(ctx context.Context, exchange, routingKey string, contentType ContentType, body []byte) error {
	c.mu.RLock()
	ch := c.ch
	c.mu.RUnlock()

	if ch == nil || ch.IsClosed() {
		return fmt.Errorf("rabbitmq: channel not available")
	}

	pubCtx, cancel := context.WithTimeout(ctx, c.cfg.publishTimeout())
	defer cancel()

	err := ch.PublishWithContext(
		pubCtx,
		exchange,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  string(contentType),
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("rabbitmq: publish to %q/%q: %w", exchange, routingKey, err)
	}
	return nil
}

// PublishWithType is like [Publish] but also sets the AMQP message type field,
// useful for event-driven architectures where consumers route on message type.
func (c *Component) PublishWithType(ctx context.Context, exchange, routingKey string, contentType ContentType, messageType string, body []byte) error {
	c.mu.RLock()
	ch := c.ch
	c.mu.RUnlock()

	if ch == nil || ch.IsClosed() {
		return fmt.Errorf("rabbitmq: channel not available")
	}

	pubCtx, cancel := context.WithTimeout(ctx, c.cfg.publishTimeout())
	defer cancel()

	err := ch.PublishWithContext(
		pubCtx,
		exchange,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  string(contentType),
			Type:         messageType,
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("rabbitmq: publish to %q/%q: %w", exchange, routingKey, err)
	}
	return nil
}

// bindAndConsume declares the queue, binds it to the exchange with the given
// routing key, and starts a consumer goroutine that exits when ctx is cancelled.
func (c *Component) bindAndConsume(ctx context.Context, ch *amqp.Channel, sub subscription) error {
	if _, err := ch.QueueDeclare(
		sub.queue,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,
	); err != nil {
		return fmt.Errorf("rabbitmq: declare queue %q: %w", sub.queue, err)
	}

	if err := ch.QueueBind(
		sub.queue,
		sub.routingKey,
		sub.exchange,
		false, // noWait
		nil,
	); err != nil {
		return fmt.Errorf("rabbitmq: bind queue %q (key %q) to exchange %q: %w", sub.queue, sub.routingKey, sub.exchange, err)
	}

	msgs, err := ch.Consume(
		sub.queue,
		"",    // consumer tag — auto-generated by broker
		false, // autoAck=false — we ack/nack manually
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,
	)
	if err != nil {
		return fmt.Errorf("rabbitmq: consume %q: %w", sub.queue, err)
	}

	go func() {
		c.log.Debug("rabbitmq: consumer started", "queue", sub.queue, "exchange", sub.exchange)
		for {
			select {
			case <-ctx.Done():
				c.log.Debug("rabbitmq: consumer stopping", "queue", sub.queue)
				return
			case d, ok := <-msgs:
				if !ok {
					// Broker closed the delivery channel (connection drop etc.).
					// The health check detects this and the supervisor triggers
					// a restart, which re-binds this consumer.
					c.log.Warn("rabbitmq: delivery channel closed", "queue", sub.queue)
					return
				}
				if err := sub.handler(d); err != nil {
					c.log.Error("rabbitmq: handler error — nacking with requeue",
						"queue", sub.queue, "error", err)
					if nackErr := d.Nack(false, true); nackErr != nil {
						c.log.Error("rabbitmq: nack failed", "queue", sub.queue, "error", nackErr)
					}
				} else {
					if ackErr := d.Ack(false); ackErr != nil {
						c.log.Error("rabbitmq: ack failed", "queue", sub.queue, "error", ackErr)
					}
				}
			}
		}
	}()

	c.log.Debug("rabbitmq: consumer bound", "queue", sub.queue, "exchange", sub.exchange)
	return nil
}
