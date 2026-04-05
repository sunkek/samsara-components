// Package rabbitmq provides a [github.com/sunkek/samsara]-compatible
// RabbitMQ component backed by [amqp091-go].
//
// # Usage
//
//	mq := rabbitmq.New(rabbitmq.Config{
//	    Host:  "localhost",
//	    Port:  5672,
//	    VHost: "myapp",
//	    User:  "myuser",
//	    Pass:  "secret",
//	})
//	sup.Add(mq,
//	    samsara.WithTier(samsara.TierCritical),
//	    samsara.WithRestartPolicy(samsara.ExponentialBackoff(5, time.Second)),
//	)
//
// Exchanges and subscriptions can be registered at any time — before or after
// [samsara.Application.Run]. If the component is already running, they take
// effect immediately on the live channel. On each restart the component
// re-declares all registered exchanges and re-binds all subscriptions
// automatically.
package rabbitmq

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Config holds all connection parameters for the RabbitMQ component.
type Config struct {
	// Host is the broker hostname or IP. Defaults to "localhost".
	Host string
	// Port is the AMQP port. Defaults to 5672.
	Port int
	// VHost is the virtual host to connect to. Defaults to "/".
	VHost string
	// User is the AMQP username. Defaults to "guest".
	User string
	// Pass is the AMQP password. Special characters are safely encoded.
	Pass string

	// URI overrides all individual fields when non-empty.
	// Must be a valid AMQP URI (amqp:// or amqps://).
	URI string

	// ConnectTimeout is the deadline for the initial dial during Start.
	// Defaults to 10 s.
	ConnectTimeout time.Duration

	// PublishTimeout is the per-attempt deadline for Publish calls.
	// Defaults to 5 s.
	PublishTimeout time.Duration
}

func (c Config) uri() string {
	if c.URI != "" {
		return c.URI
	}
	host := c.Host
	if host == "" {
		host = "localhost"
	}
	port := c.Port
	if port == 0 {
		port = 5672
	}
	vhost := c.VHost
	if vhost == "" {
		vhost = "/"
	}
	user := c.User
	if user == "" {
		user = "guest"
	}
	// url.UserPassword percent-encodes credentials, preventing URI injection
	// from passwords containing @, /, or other reserved characters.
	u := &url.URL{
		Scheme: "amqp",
		User:   url.UserPassword(user, c.Pass),
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   vhost,
	}
	return u.String()
}

func (c Config) connectTimeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return 10 * time.Second
}

func (c Config) publishTimeout() time.Duration {
	if c.PublishTimeout > 0 {
		return c.PublishTimeout
	}
	return 5 * time.Second
}

// ExchangeKind is the AMQP exchange type.
type ExchangeKind string

const (
	ExchangeDirect  ExchangeKind = amqp.ExchangeDirect
	ExchangeTopic   ExchangeKind = amqp.ExchangeTopic
	ExchangeFanout  ExchangeKind = amqp.ExchangeFanout
	ExchangeHeaders ExchangeKind = amqp.ExchangeHeaders
)

// exchangeDecl stores the parameters for a declared exchange so it can be
// re-declared on restart.
type exchangeDecl struct {
	name    string
	kind    ExchangeKind
	durable bool
}

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}
func (nopLogger) Debug(string, ...any) {}

// Component is a samsara-compatible RabbitMQ component.
// Obtain one with [New]; register it with a samsara supervisor.
type Component struct {
	cfg  Config
	log  Logger
	name string

	// mu guards conn, ch, and stopCh across the Start/Stop/restart lifecycle.
	mu     sync.RWMutex
	conn   *amqp.Connection
	ch     *amqp.Channel
	stopCh chan struct{}

	// exchMu guards exchanges, which are registered before Start and read
	// during Start (re-declaration on restart). Separate from mu to avoid
	// holding the broad lock during topology setup.
	exchMu    sync.RWMutex
	exchanges []exchangeDecl

	// subsMu guards subscriptions similarly.
	subsMu sync.RWMutex
	subs   []subscription
}

// New creates a Component from the supplied config.
// The component is not connected until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "rabbitmq",
		stopCh: make(chan struct{}), // initialised so Stop-before-Start is safe
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Option configures a [Component].
type Option func(*Component)

// WithLogger attaches a structured logger to the component.
// [log/slog.Logger] satisfies [Logger] directly.
func WithLogger(l Logger) Option {
	return func(c *Component) { c.log = l }
}

// WithName overrides the component name returned by [Component.Name].
// Useful when connecting to multiple brokers with the same supervisor.
func WithName(name string) Option {
	return func(c *Component) { c.name = name }
}

// Compile-time assertion: *Component satisfies the samsara component and
// health-checker interfaces without importing the samsara package.
var (
	_ interface {
		Name() string
		Start(ctx context.Context, ready func()) error
		Stop(ctx context.Context) error
	} = (*Component)(nil)

	_ interface {
		Health(ctx context.Context) error
	} = (*Component)(nil)
)

// Name implements samsara.Component.
func (c *Component) Name() string { return c.name }

// Start dials the broker, opens a channel, re-declares all exchanges and
// re-binds all subscriptions, calls ready(), then blocks until Stop or ctx
// cancellation.
//
// Start is safe to call multiple times across restarts.
func (c *Component) Start(ctx context.Context, ready func()) error {
	// Allocate a fresh stopCh for this run under the write lock.
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	// amqp.DialConfig has no context support, so we race it against ctx and
	// ConnectTimeout in a goroutine. The goroutine is always given the full
	// ConnectTimeout so it can close the connection cleanly if we bail early.
	type dialResult struct {
		conn *amqp.Connection
		err  error
	}
	dialDone := make(chan dialResult, 1)
	go func() {
		conn, err := amqp.DialConfig(c.cfg.uri(), amqp.Config{
			Dial: amqp.DefaultDial(c.cfg.connectTimeout()),
		})
		dialDone <- dialResult{conn, err}
	}()

	dialTimer := time.NewTimer(c.cfg.connectTimeout())
	defer dialTimer.Stop()

	var res dialResult
	select {
	case <-ctx.Done():
		// Clean shutdown — samsara requires nil on context cancellation.
		// The dial goroutine may still succeed after we return. Drain the
		// channel in a goroutine and close any connection it produces so we
		// don't leak an open TCP connection to the broker.
		go func() {
			if r := <-dialDone; r.conn != nil {
				_ = r.conn.Close()
			}
		}()
		return nil
	case <-dialTimer.C:
		return fmt.Errorf("rabbitmq: dial timed out after %s", c.cfg.connectTimeout())
	case res = <-dialDone:
	}

	if res.err != nil {
		return fmt.Errorf("rabbitmq: dial failed: %w", res.err)
	}
	conn := res.conn

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("rabbitmq: open channel failed: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.ch = ch
	c.mu.Unlock()

	// Re-declare exchanges on every start (idempotent if parameters match).
	c.exchMu.RLock()
	exchanges := make([]exchangeDecl, len(c.exchanges))
	copy(exchanges, c.exchanges)
	c.exchMu.RUnlock()

	for _, e := range exchanges {
		if err := ch.ExchangeDeclare(
			e.name, string(e.kind), e.durable,
			false, false, false, nil,
		); err != nil {
			_ = ch.Close()
			_ = conn.Close()
			return fmt.Errorf("rabbitmq: declare exchange %q: %w", e.name, err)
		}
		c.log.Debug("rabbitmq: exchange declared", "name", e.name, "kind", e.kind)
	}

	// Re-bind subscriptions.
	c.subsMu.RLock()
	subs := make([]subscription, len(c.subs))
	copy(subs, c.subs)
	c.subsMu.RUnlock()

	for _, sub := range subs {
		if err := c.bindAndConsume(ctx, ch, sub); err != nil {
			// A failed subscription is logged but not fatal — the component
			// can still publish and other subscriptions still work. The health
			// check will surface problems via the channel state.
			c.log.Error("rabbitmq: subscription failed",
				"exchange", sub.exchange, "queue", sub.queue, "error", err)
		}
	}

	c.log.Info("rabbitmq: connected", "host", c.cfg.Host, "vhost", c.cfg.VHost)
	ready()

	select {
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// Stop signals Start to return and closes the channel and connection.
// It is idempotent and concurrency-safe.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	conn := c.conn
	channel := c.ch
	c.mu.Unlock()

	// Signal the running Start goroutine to exit.
	select {
	case <-ch:
	default:
		close(ch)
	}

	// Close channel then connection; honour the stop deadline.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if channel != nil {
			if err := channel.Close(); err != nil && !isAlreadyClosed(err) {
				c.log.Error("rabbitmq: channel close error", "error", err)
			}
		}
		if conn != nil {
			if err := conn.Close(); err != nil && !isAlreadyClosed(err) {
				c.log.Error("rabbitmq: connection close error", "error", err)
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
		c.log.Error("rabbitmq: close timed out during shutdown")
	}
	return nil
}

// Health implements samsara.HealthChecker.
// Returns a non-nil error if the connection or channel is closed.
func (c *Component) Health(_ context.Context) error {
	c.mu.RLock()
	conn := c.conn
	ch := c.ch
	c.mu.RUnlock()

	if conn == nil || conn.IsClosed() {
		return fmt.Errorf("rabbitmq: connection is closed")
	}
	if ch == nil || ch.IsClosed() {
		return fmt.Errorf("rabbitmq: channel is closed")
	}
	return nil
}

// isAlreadyClosed reports whether err is the amqp "Exception (504)" error
// that indicates the channel or connection was already closed by the broker.
// We suppress these in Stop to avoid noisy logs during broker-side disconnects.
func isAlreadyClosed(err error) bool {
	if err == nil {
		return false
	}
	amqpErr, ok := err.(*amqp.Error)
	return ok && amqpErr.Code == amqp.ChannelError
}
