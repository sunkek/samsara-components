package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"maps"
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

// QueueTypeQuorum is the x-queue-type value for quorum queues, which support
// broker-enforced redelivery limits via x-delivery-limit. Use it as
// [SubscribeOptions.QueueType].
const QueueTypeQuorum = "quorum"

// ErrDropToDLX is a sentinel error a handler may return (or wrap) to signal
// that the message must NOT be requeued. The component nacks with
// requeue=false, so any queue-level dead-letter-exchange policy fires and the
// message is dead-lettered instead of looping in place.
//
// A handler that returns any other error gets the default behaviour: nack with
// requeue=true (retry in place). Use errors.Join or fmt.Errorf("%w", ...) to
// attach context while preserving the drop signal.
var ErrDropToDLX = errors.New("rabbitmq: drop message to dead-letter exchange")

// SubscribeOptions carries optional queue-declaration parameters for
// [Component.SubscribeWithOptions]. The zero value reproduces the behaviour of
// [Component.Subscribe].
type SubscribeOptions struct {
	// QueueArgs are passed verbatim as the x-arguments of QueueDeclare, e.g.
	// "x-dead-letter-exchange", "x-message-ttl", "x-delivery-limit". This lets
	// the broker own dead-lettering and the redelivery cap at declare time.
	QueueArgs amqp.Table

	// QueueType sets "x-queue-type" (e.g. [QueueTypeQuorum]). When non-empty it
	// overrides any "x-queue-type" present in QueueArgs. Quorum queues are
	// required for broker-enforced "x-delivery-limit".
	QueueType string

	// Retry enables the component-managed delayed-retry pipeline for this
	// subscription. When nil, handler errors keep the legacy behaviour
	// (nack with requeue=true, or requeue=false for [ErrDropToDLX]).
	Retry *RetryPolicy
}

// RetryHeader is the AMQP header carrying the current attempt number on a
// message travelling through the retry pipeline. The first delivery has no
// header; the first retry carries int32(1), and so on.
const RetryHeader = "x-retry-count"

// RetryPolicy configures the component-managed retry pipeline used by
// [Component.SubscribeWithOptions] when [SubscribeOptions.Retry] is set.
//
// For a work queue "q" the component declares two companion queues:
//
//   - "q.retry" — the delay queue. Failed messages are republished here with a
//     per-message expiration equal to the backoff for the current attempt.
//     When the TTL fires, the broker dead-letters the message back to "q"
//     through the default exchange, producing a delayed retry.
//   - "q.dlq" — the terminal dead-letter queue. Messages land here after
//     MaxRetries attempts are exhausted, or immediately when the handler
//     returns [ErrDropToDLX].
//
// All zero-value fields fall back to documented defaults, mirroring [Config].
type RetryPolicy struct {
	// MaxRetries is the number of retries after the initial delivery.
	// Defaults to 3. Negative values mean no retries (straight to the DLQ).
	MaxRetries int

	// Backoff is the delay before the first retry. Defaults to 1 s.
	Backoff time.Duration

	// BackoffMultiplier scales the delay for each subsequent retry
	// (exponential backoff). Defaults to 2. Values < 1 are treated as 1
	// (constant backoff).
	BackoffMultiplier float64

	// MaxBackoff caps the per-retry delay. Defaults to 5 min.
	MaxBackoff time.Duration

	// RetryQueue overrides the delay queue name. Defaults to queue + ".retry".
	RetryQueue string

	// DLQ overrides the terminal dead-letter queue name.
	// Defaults to queue + ".dlq".
	DLQ string
}

func (p RetryPolicy) maxRetries() int {
	if p.MaxRetries != 0 {
		return max(p.MaxRetries, 0)
	}
	return 3
}

func (p RetryPolicy) retryQueue(queue string) string {
	if p.RetryQueue != "" {
		return p.RetryQueue
	}
	return queue + ".retry"
}

func (p RetryPolicy) dlq(queue string) string {
	if p.DLQ != "" {
		return p.DLQ
	}
	return queue + ".dlq"
}

// backoff returns the delay before retry number attempt (1-based).
func (p RetryPolicy) backoff(attempt int) time.Duration {
	base := p.Backoff
	if base <= 0 {
		base = time.Second
	}
	mult := p.BackoffMultiplier
	if mult < 1 {
		if mult == 0 {
			mult = 2
		} else {
			mult = 1
		}
	}
	maxB := p.MaxBackoff
	if maxB <= 0 {
		maxB = 5 * time.Minute
	}
	d := float64(base)
	for i := 1; i < attempt; i++ {
		d *= mult
		if d >= float64(maxB) {
			return maxB
		}
	}
	return min(time.Duration(d), maxB)
}

// subscription describes a queue binding and its message handler.
// The consumer goroutine's lifetime is tied to the component context passed
// into Start, not to an arbitrary caller context.
type subscription struct {
	routingKey string
	exchange   string
	queue      string
	queueArgs  amqp.Table
	retry      *RetryPolicy
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
	return c.SubscribeWithOptions(exchange, queue, routingKey, handler, SubscribeOptions{})
}

// SubscribeWithOptions is like [SubscribeWithKey] but declares the queue with
// the parameters in opts, allowing native dead-letter and redelivery-cap setups
// (x-dead-letter-exchange, x-delivery-limit, quorum queues) at declare time.
//
// The queue must not already exist with conflicting arguments; AMQP rejects a
// redeclare whose x-arguments differ from the live queue.
func (c *Component) SubscribeWithOptions(exchange, queue, routingKey string, handler func(amqp.Delivery) error, opts SubscribeOptions) error {
	sub := subscription{
		exchange:   exchange,
		queue:      queue,
		routingKey: routingKey,
		queueArgs:  buildQueueArgs(opts),
		retry:      opts.Retry,
		handler:    handler,
	}

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

// Publisher is the interface that domain adapters should depend on.
// *Component satisfies it; depend on Publisher rather than *Component to keep
// adapters testable without a real broker.
//
//	type OrderEvents struct { mq rabbitmq.Publisher }
//
// Subscription setup (DeclareExchange, Subscribe) is wiring, not a domain
// concern, so it stays off this interface.
type Publisher interface {
	// Publish sends a message to the given exchange with the given routing key.
	// See [Component.Publish].
	Publish(ctx context.Context, exchange, routingKey string, contentType ContentType, body []byte) error

	// PublishWithType also sets the AMQP message type field.
	PublishWithType(ctx context.Context, exchange, routingKey string, contentType ContentType, messageType string, body []byte) error

	// PublishWithHeaders stamps the given AMQP headers on the message.
	PublishWithHeaders(ctx context.Context, exchange, routingKey string, contentType ContentType, headers amqp.Table, body []byte) error
}

// Compile-time assertion: *Component satisfies Publisher.
var _ Publisher = (*Component)(nil)

// Publish sends a message to the given exchange with the given routing key.
// It respects ctx for cancellation and uses the configured PublishTimeout
// as a per-attempt deadline.
//
// Publish does not retry internally. If you need retry logic, wrap this call
// in your own retry loop — the appropriate strategy (retry, dead-letter, drop)
// is a domain concern, not an infrastructure one.
func (c *Component) Publish(ctx context.Context, exchange, routingKey string, contentType ContentType, body []byte) error {
	return c.observePublish(opPublish, func() error {
		return c.publish(ctx, exchange, routingKey, amqp.Publishing{
			ContentType:  string(contentType),
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		})
	})
}

// PublishWithType is like [Publish] but also sets the AMQP message type field,
// useful for event-driven architectures where consumers route on message type.
func (c *Component) PublishWithType(ctx context.Context, exchange, routingKey string, contentType ContentType, messageType string, body []byte) error {
	return c.observePublish(opPublishWithType, func() error {
		return c.publish(ctx, exchange, routingKey, amqp.Publishing{
			ContentType:  string(contentType),
			Type:         messageType,
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		})
	})
}

// PublishWithHeaders is like [Publish] but stamps the given AMQP headers on the
// message, letting callers carry custom metadata (e.g. an attempt counter) on a
// republished message.
func (c *Component) PublishWithHeaders(ctx context.Context, exchange, routingKey string, contentType ContentType, headers amqp.Table, body []byte) error {
	return c.observePublish(opPublishWithHeaders, func() error {
		return c.publish(ctx, exchange, routingKey, amqp.Publishing{
			ContentType:  string(contentType),
			Headers:      headers,
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		})
	})
}

// publish is the shared publish path: it validates the live channel, applies
// the per-attempt PublishTimeout, and wraps broker errors with context.
func (c *Component) publish(ctx context.Context, exchange, routingKey string, msg amqp.Publishing) error {
	c.mu.RLock()
	ch := c.ch
	c.mu.RUnlock()

	if ch == nil || ch.IsClosed() {
		return fmt.Errorf("rabbitmq: channel not available")
	}

	pubCtx, cancel := context.WithTimeout(ctx, c.cfg.publishTimeout())
	defer cancel()

	if err := ch.PublishWithContext(
		pubCtx,
		exchange,
		routingKey,
		false, // mandatory
		false, // immediate
		msg,
	); err != nil {
		return fmt.Errorf("rabbitmq: publish to %q/%q: %w", exchange, routingKey, err)
	}
	return nil
}

// buildQueueArgs merges SubscribeOptions into an amqp.Table for QueueDeclare.
// It returns nil for the zero-value options so the declare matches the legacy
// nil-argument behaviour exactly. QueueType, when set, overrides any
// x-queue-type in QueueArgs.
func buildQueueArgs(opts SubscribeOptions) amqp.Table {
	if len(opts.QueueArgs) == 0 && opts.QueueType == "" {
		return nil
	}
	args := amqp.Table{}
	maps.Copy(args, opts.QueueArgs)
	if opts.QueueType != "" {
		args["x-queue-type"] = opts.QueueType
	}
	return args
}

// declareRetryTopology declares the delay queue and terminal DLQ for a
// subscription with a retry policy. The delay queue dead-letters expired
// messages back to the work queue through the default exchange.
func (c *Component) declareRetryTopology(ch *amqp.Channel, sub subscription) error {
	retryQueue := sub.retry.retryQueue(sub.queue)
	if _, err := ch.QueueDeclare(
		retryQueue,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		amqp.Table{
			"x-dead-letter-exchange":    "", // default exchange
			"x-dead-letter-routing-key": sub.queue,
		},
	); err != nil {
		return fmt.Errorf("rabbitmq: declare retry queue %q: %w", retryQueue, err)
	}

	dlq := sub.retry.dlq(sub.queue)
	if _, err := ch.QueueDeclare(
		dlq,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,
	); err != nil {
		return fmt.Errorf("rabbitmq: declare dead-letter queue %q: %w", dlq, err)
	}

	c.log.Debug("rabbitmq: retry topology declared",
		"queue", sub.queue, "retryQueue", retryQueue, "dlq", dlq)
	return nil
}

// bindAndConsume declares the queue, binds it to the exchange with the given
// routing key, and starts a consumer goroutine that exits when ctx is cancelled.
func (c *Component) bindAndConsume(ctx context.Context, ch *amqp.Channel, sub subscription) error {
	if sub.retry != nil {
		if err := c.declareRetryTopology(ch, sub); err != nil {
			return err
		}
	}

	if _, err := ch.QueueDeclare(
		sub.queue,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		sub.queueArgs,
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
					// Broker closed the delivery channel (connection drop,
					// consumer cancel, queue deletion). If the component is
					// still running this consumer is silently dead — record it
					// so Health reports unhealthy and the supervisor restarts
					// the component, re-binding this consumer.
					select {
					case <-ctx.Done():
						// Normal shutdown/restart — not a failure.
					default:
						c.log.Error("rabbitmq: delivery channel closed while running",
							"queue", sub.queue)
						c.markConsumerDead(sub.queue, "delivery channel closed by broker")
					}
					return
				}
				if err := sub.handler(d); err != nil {
					c.handleFailure(ctx, sub, d, err)
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

// handleFailure applies the failure policy for a delivery whose handler
// returned a non-nil error.
//
// Without a retry policy, the legacy behaviour applies: nack with
// requeue=true, or requeue=false when the handler signals [ErrDropToDLX].
//
// With a retry policy, the component republishes the message to the delay
// queue with a per-message expiration (delayed retry) until MaxRetries is
// exhausted, then moves it to the terminal DLQ. [ErrDropToDLX] short-circuits
// straight to the DLQ.
func (c *Component) handleFailure(ctx context.Context, sub subscription, d amqp.Delivery, handlerErr error) {
	if sub.retry == nil {
		requeue := !errors.Is(handlerErr, ErrDropToDLX)
		c.log.Error("rabbitmq: handler error — nacking",
			"queue", sub.queue, "requeue", requeue, "error", handlerErr)
		if nackErr := d.Nack(false, requeue); nackErr != nil {
			c.log.Error("rabbitmq: nack failed", "queue", sub.queue, "error", nackErr)
		}
		return
	}

	policy := sub.retry
	attempt := retryCount(d.Headers) // retries already performed
	drop := errors.Is(handlerErr, ErrDropToDLX)

	if drop || attempt >= policy.maxRetries() {
		// Terminal: move to the DLQ and ack the original.
		dlq := policy.dlq(sub.queue)
		c.log.Error("rabbitmq: handler error — dead-lettering",
			"queue", sub.queue, "dlq", dlq, "attempts", attempt+1,
			"dropRequested", drop, "error", handlerErr)
		if err := c.republish(ctx, d, "", dlq, attempt, 0); err != nil {
			// Could not persist to the DLQ; keep the message in the work
			// queue rather than losing it.
			c.log.Error("rabbitmq: dead-letter publish failed — requeueing",
				"queue", sub.queue, "dlq", dlq, "error", err)
			if nackErr := d.Nack(false, true); nackErr != nil {
				c.log.Error("rabbitmq: nack failed", "queue", sub.queue, "error", nackErr)
			}
			return
		}
	} else {
		// Delayed retry via the TTL queue.
		delay := policy.backoff(attempt + 1)
		retryQueue := policy.retryQueue(sub.queue)
		c.log.Warn("rabbitmq: handler error — scheduling retry",
			"queue", sub.queue, "attempt", attempt+1, "maxRetries", policy.maxRetries(),
			"delay", delay, "error", handlerErr)
		if err := c.republish(ctx, d, "", retryQueue, attempt+1, delay); err != nil {
			c.log.Error("rabbitmq: retry publish failed — requeueing",
				"queue", sub.queue, "retryQueue", retryQueue, "error", err)
			if nackErr := d.Nack(false, true); nackErr != nil {
				c.log.Error("rabbitmq: nack failed", "queue", sub.queue, "error", nackErr)
			}
			return
		}
	}

	if ackErr := d.Ack(false); ackErr != nil {
		c.log.Error("rabbitmq: ack after republish failed", "queue", sub.queue, "error", ackErr)
	}
}

// republish clones a delivery to exchange/routingKey, stamping the retry
// counter header and, when expiration > 0, a per-message TTL.
func (c *Component) republish(ctx context.Context, d amqp.Delivery, exchange, routingKey string, retries int, expiration time.Duration) error {
	headers := amqp.Table{}
	maps.Copy(headers, d.Headers)
	headers[RetryHeader] = int32(retries)

	msg := amqp.Publishing{
		Headers:       headers,
		ContentType:   d.ContentType,
		DeliveryMode:  amqp.Persistent,
		CorrelationId: d.CorrelationId,
		MessageId:     d.MessageId,
		Timestamp:     d.Timestamp,
		Type:          d.Type,
		AppId:         d.AppId,
		Body:          d.Body,
	}
	if expiration > 0 {
		msg.Expiration = fmt.Sprintf("%d", expiration.Milliseconds())
	}
	return c.publish(ctx, exchange, routingKey, msg)
}

// retryCount extracts the retry counter from message headers, tolerating the
// integer widths different AMQP clients use.
func retryCount(headers amqp.Table) int {
	v, ok := headers[RetryHeader]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int32:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
