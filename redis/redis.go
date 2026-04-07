// Package redis provides a [github.com/sunkek/samsara]-compatible Redis
// component backed by [go-redis/v9].
//
// # Usage
//
//	rdb := redis.New(redis.Config{
//	    Host: "localhost",
//	    Port: 6379,
//	})
//	sup.Add(rdb,
//	    samsara.WithTier(samsara.TierCritical),
//	    samsara.WithRestartPolicy(samsara.ExponentialBackoff(5, time.Second)),
//	)
//
// Domain adapters receive *Component (or the [Client] interface) and call
// Set, Get, Del, and Scan — they never import go-redis directly.
package redis

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config holds all connection parameters for the Redis component.
type Config struct {
	// Host is the Redis server hostname or IP. Defaults to "localhost".
	Host string
	// Port is the Redis server port. Defaults to 6379.
	Port int
	// DB is the Redis database number to select. Defaults to 0.
	DB int
	// User is the ACL username. Leave empty for no authentication or
	// when using password-only auth (Redis < 6).
	User string
	// Pass is the Redis password or ACL user password.
	Pass string

	// ConnectTimeout is the deadline for the initial PING during Start.
	// Defaults to 10 s.
	ConnectTimeout time.Duration

	// DialTimeout is the timeout for establishing each new connection.
	// Defaults to go-redis default (5 s).
	DialTimeout time.Duration
	// ReadTimeout is the timeout for socket reads.
	// Defaults to go-redis default (3 s).
	ReadTimeout time.Duration
	// WriteTimeout is the timeout for socket writes.
	// Defaults to go-redis default (ReadTimeout).
	WriteTimeout time.Duration

	// PoolSize is the maximum number of connections in the pool.
	// Defaults to 10 per CPU.
	PoolSize int
}

func (c Config) addr() string {
	host := c.Host
	if host == "" {
		host = "localhost"
	}
	port := c.Port
	if port == 0 {
		port = 6379
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func (c Config) connectTimeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return 10 * time.Second
}

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// Component is a samsara-compatible Redis component.
// Obtain one with [New]; register it with a samsara supervisor.
//
// Domain adapters should accept [Client] rather than *Component to keep
// their tests independent of a real Redis server.
type Component struct {
	cfg  Config
	log  Logger
	name string

	// mu guards client and stopCh across Start/Stop/restart.
	mu     sync.RWMutex
	client *redis.Client
	stopCh chan struct{}
}

// New creates a Component from the supplied config.
// The component is not connected until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "redis",
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
// Useful when connecting to multiple Redis instances with the same supervisor.
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

// getClient returns the current client under a read lock.
func (c *Component) getClient() *redis.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

// Start creates the Redis client, pings the server to confirm connectivity,
// calls ready() to unblock the supervisor, then blocks until Stop or ctx
// cancellation.
//
// Start is safe to call multiple times across restarts.
func (c *Component) Start(ctx context.Context, ready func()) error {
	// Allocate a fresh stopCh for this run under the write lock.
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	opts := &redis.Options{
		Addr:         c.cfg.addr(),
		Username:     c.cfg.User,
		Password:     c.cfg.Pass,
		DB:           c.cfg.DB,
		DialTimeout:  c.cfg.DialTimeout,
		ReadTimeout:  c.cfg.ReadTimeout,
		WriteTimeout: c.cfg.WriteTimeout,
		PoolSize:     c.cfg.PoolSize,
	}
	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, c.cfg.connectTimeout())
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return fmt.Errorf("redis: ping failed: %w", err)
	}

	c.mu.Lock()
	c.client = client
	c.mu.Unlock()

	c.log.Info("redis: connected", "addr", c.cfg.addr(), "db", c.cfg.DB)
	ready()

	select {
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// Stop signals Start to return and closes the client connection pool.
// It is idempotent and concurrency-safe.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	client := c.client
	c.mu.Unlock()

	// Signal the running Start goroutine to exit.
	select {
	case <-ch:
	default:
		close(ch)
	}

	if client == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- client.Close() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("redis: close: %w", err)
		}
		return nil
	case <-ctx.Done():
		c.log.Error("redis: client close timed out during shutdown")
		return nil
	}
}

// Health implements samsara.HealthChecker.
// Returns a non-nil error if the server does not respond to PING.
func (c *Component) Health(ctx context.Context) error {
	client := c.getClient()
	if client == nil {
		return fmt.Errorf("redis: client not initialised")
	}
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping: %w", err)
	}
	return nil
}
