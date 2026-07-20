// Package sqlite provides a [github.com/sunkek/samsara]-compatible SQLite
// component backed by the pure-Go modernc.org/sqlite driver, so services using
// it still build with CGO_ENABLED=0.
//
// # Usage
//
//	comp := sqlite.New(sqlite.Config{Path: "/var/lib/myapp/data.db"})
//	sup.Add(comp, samsara.WithTier(samsara.TierCritical))
//
// Domain adapters receive the [DB] interface and use its Select, Get, Exec,
// and transaction helpers — they never import database/sql or the driver.
//
// # Concurrency
//
// SQLite allows one writer at a time. The component defaults to a pool of a
// single connection, which serialises writes inside the pool where they queue
// instead of failing. See [Config.MaxOpenConns] before raising it.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// Component is a samsara-compatible SQLite component.
// Obtain one with [New]; register it with a samsara supervisor.
//
// Domain adapters should accept [DB] rather than *Component to keep their
// tests independent of a real database.
type Component struct {
	cfg  Config
	log  Logger
	name string

	// mu guards db and stopCh across the Start/Stop/restart lifecycle.
	mu     sync.RWMutex
	db     *sql.DB
	stopCh chan struct{}
}

// New creates a Component from the supplied config.
// No file is opened until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "sqlite",
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
// Useful when registering multiple SQLite components with the same supervisor
// (e.g. an application database and a separate cache database).
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

// getDB returns the current handle under a read lock.
func (c *Component) getDB() *sql.DB {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.db
}

// Start opens the database, verifies it is actually usable, calls ready() to
// unblock the supervisor, then blocks until Stop or ctx cancellation.
//
// Verification matters more here than for a networked store: sql.Open is lazy
// and never touches the filesystem, so a misconfigured path or an unwritable
// volume would otherwise surface as a failed query long after the supervisor
// had been told the component was ready.
//
// Start is safe to call multiple times across restarts; each call allocates a
// fresh stopCh so the previous Stop signal does not bleed into the new run.
func (c *Component) Start(ctx context.Context, ready func()) error {
	// Allocate a fresh stopCh for this run under the write lock, so a
	// concurrent Stop always operates on a valid, current channel.
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	if !c.cfg.inMemory() {
		if dir := filepath.Dir(c.cfg.path()); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("sqlite: create data directory %q: %w", dir, err)
			}
		}
	}

	db, err := sql.Open("sqlite", c.cfg.dsn())
	if err != nil {
		return fmt.Errorf("sqlite: open %q: %w", c.cfg.path(), err)
	}
	db.SetMaxOpenConns(c.cfg.maxOpenConns())

	connectCtx, cancel := context.WithTimeout(ctx, c.cfg.connectTimeout())
	defer cancel()

	if err := db.PingContext(connectCtx); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite: ping %q: %w", c.cfg.path(), err)
	}

	if err := c.verifyJournalMode(connectCtx, db); err != nil {
		_ = db.Close()
		return err
	}

	c.mu.Lock()
	c.db = db
	c.mu.Unlock()

	c.log.Info("sqlite: ready", "path", c.cfg.path(), "journal_mode", c.cfg.journalMode())

	ready()

	select {
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// verifyJournalMode confirms the requested journal mode actually took effect.
// SQLite silently falls back to a rollback journal when WAL is unavailable —
// most commonly on network filesystems, which lack the shared-memory primitive
// WAL needs. Discovering that at startup is far cheaper than discovering it as
// unexplained SQLITE_BUSY errors under concurrent load in production.
func (c *Component) verifyJournalMode(ctx context.Context, db *sql.DB) error {
	want := c.cfg.journalMode()

	var got string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&got); err != nil {
		return fmt.Errorf("sqlite: read journal_mode: %w", err)
	}
	if strings.EqualFold(got, want) {
		return nil
	}

	// An in-memory database always reports "memory" and cannot use WAL. That is
	// inherent to the mode rather than a misconfiguration, so it is not an error.
	if c.cfg.inMemory() {
		c.log.Debug("sqlite: in-memory database uses memory journal", "requested", want)
		return nil
	}

	return fmt.Errorf("sqlite: journal_mode is %q but %q was requested; "+
		"WAL is unavailable on this filesystem (network mounts do not support it)", got, want)
}

// Stop signals Start to return and closes the database handle.
// It is idempotent and concurrency-safe: multiple concurrent calls are safe,
// and calling Stop before Start has been called is safe.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	// Replace stopCh with a pre-closed channel so subsequent Stop calls and any
	// future Start that races with this Stop see a consistent state.
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	db := c.db
	c.db = nil // nil-clear so post-stop callers get "not initialised"
	c.mu.Unlock()

	// Signal the currently-running Start (if any) to exit.
	select {
	case <-ch:
		// Already closed — either Stop was called before Start, or a previous
		// Stop already signalled. Nothing to do.
	default:
		close(ch)
	}

	if db != nil {
		done := make(chan struct{})
		go func() {
			// Close waits for in-flight queries to finish. On a WAL database it
			// also checkpoints, which is why it is worth bounding by ctx.
			if err := db.Close(); err != nil {
				c.log.Error("sqlite: close failed", "error", err)
			}
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			c.log.Warn("sqlite: close timed out during shutdown")
		}
	}
	return nil
}

// Health implements samsara.HealthChecker.
//
// It runs a real query rather than a ping: database/sql can satisfy a ping from
// a pooled connection whose underlying file has been deleted or replaced, so a
// ping alone would report a healthy component with an unusable database.
func (c *Component) Health(ctx context.Context) error {
	db := c.getDB()
	if db == nil {
		return fmt.Errorf("sqlite: not initialised")
	}
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("sqlite: health query: %w", err)
	}
	if one != 1 {
		return fmt.Errorf("sqlite: health query returned %d, want 1", one)
	}
	return nil
}
