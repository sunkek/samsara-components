// Package postgresql provides a [github.com/sunkek/samsara]-compatible
// PostgreSQL component backed by pgx/v5 connection pooling.
//
// # Usage
//
//	comp := postgresql.New(postgresql.Config{
//	    Host: "localhost",
//	    Port: 5432,
//	    Name: "mydb",
//	    User: "myuser",
//	    Pass: "secret",
//	})
//	sup.Add(comp, samsara.WithTier(samsara.TierCritical))
//
// Domain adapters receive *Component (or the [DB] interface) and use its
// Select, Get, Exec, and transaction helpers — they never import pgx or
// pgxscan directly.
package postgresql

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds all connection parameters for the PostgreSQL component.
// Supply individual fields or a preformatted [Config.URI] to override them all.
type Config struct {
	// Host is the database server hostname or IP. Defaults to "localhost".
	Host string
	// Port is the database server port. Defaults to 5432.
	Port int
	// Name is the database name. Defaults to "postgres".
	Name string
	// User is the database user. Defaults to "postgres".
	User string
	// Pass is the database password.
	Pass string

	// SSLMode controls the SSL negotiation mode (disable, require, verify-ca,
	// verify-full). Defaults to "disable".
	SSLMode string

	// URI overrides all individual fields when non-empty.
	// Must be a valid libpq connection string or pgx DSN.
	URI string

	// ConnectTimeout is the deadline for the initial connection + ping during
	// Start. Defaults to 10 s.
	ConnectTimeout time.Duration

	// MaxConns caps the pool size. 0 means pgx default (min(4, GOMAXPROCS)).
	MaxConns int32
	// MinConns keeps this many connections alive even when idle. 0 means none.
	MinConns int32
}

func (c Config) dsn() string {
	if c.URI != "" {
		return c.URI
	}
	host := c.Host
	if host == "" {
		host = "localhost"
	}
	port := c.Port
	if port == 0 {
		port = 5432
	}
	name := c.Name
	if name == "" {
		name = "postgres"
	}
	user := c.User
	if user == "" {
		user = "postgres"
	}
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		user, c.Pass, host, port, name, ssl,
	)
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

// Component is a samsara-compatible PostgreSQL component.
// Obtain one with [New]; register it with a samsara supervisor.
//
// Domain adapters should accept [DB] rather than *Component to keep
// their tests independent of a real database.
type Component struct {
	cfg  Config
	log  Logger
	name string
	pool *pgxpool.Pool

	// mu guards stopCh across the Start/Stop/restart lifecycle.
	// Start holds a read lock while blocking; Stop acquires a write lock
	// only to replace stopCh before signalling, so concurrent calls are safe.
	mu     sync.Mutex
	stopCh chan struct{}
}

// New creates a Component from the supplied config.
// The component is not connected until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "postgres",
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
// Useful when registering multiple PostgreSQL components with the same
// supervisor (e.g. a primary and a read replica).
func WithName(name string) Option {
	return func(c *Component) { c.name = name }
}

// Compile-time assertion: *Component satisfies the samsara component and
// health-checker interfaces without importing the samsara package.
// If samsara ever changes its interface signatures, this breaks at compile
// time here rather than at runtime in the caller's code.
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

// Start creates the connection pool, pings the server to confirm reachability,
// calls ready() to unblock the supervisor, then blocks until Stop or ctx
// cancellation.
//
// Start is safe to call multiple times across restarts; each call allocates a
// fresh stopCh so the previous Stop signal does not bleed into the new run.
func (c *Component) Start(ctx context.Context, ready func()) error {
	// Allocate a fresh stopCh for this run. Must happen before any blocking
	// work so a concurrent Stop always has a valid channel to close.
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	poolCfg, err := pgxpool.ParseConfig(c.cfg.dsn())
	if err != nil {
		return fmt.Errorf("postgres: invalid DSN: %w", err)
	}
	if c.cfg.MaxConns > 0 {
		poolCfg.MaxConns = c.cfg.MaxConns
	}
	if c.cfg.MinConns > 0 {
		poolCfg.MinConns = c.cfg.MinConns
	}

	connectCtx, cancel := context.WithTimeout(ctx, c.cfg.connectTimeout())
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return fmt.Errorf("postgres: pool creation failed: %w", err)
	}

	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return fmt.Errorf("postgres: ping failed: %w", err)
	}

	c.pool = pool
	c.log.Info("postgres: connected", "host", c.cfg.Host)

	ready()

	select {
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// Stop signals Start to return and closes the connection pool.
// It is idempotent and concurrency-safe: multiple concurrent calls are safe,
// and calling Stop before Start has been called is safe.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	// Replace stopCh with a pre-closed channel so subsequent Stop calls and
	// any future Start that races with this Stop see a consistent state.
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	c.mu.Unlock()

	// Signal the currently-running Start (if any) to exit.
	select {
	case <-ch:
		// Already closed — either Stop was called before Start, or a previous
		// Stop already signalled. Nothing to do.
	default:
		close(ch)
	}

	if c.pool != nil {
		done := make(chan struct{})
		go func() {
			c.pool.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			c.log.Error("postgres: pool close timed out during shutdown")
		}
	}
	return nil
}

// Health implements samsara.HealthChecker.
// Returns a non-nil error if the pool cannot reach the database.
func (c *Component) Health(ctx context.Context) error {
	if c.pool == nil {
		return fmt.Errorf("postgres: pool not initialised")
	}
	return c.pool.Ping(ctx)
}
