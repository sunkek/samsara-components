package postgresql

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Operation names reported to [Config.OnOperation]. They are fixed per method
// and never derived from a SQL string, which is unbounded and would blow up
// label cardinality in the sink.
// See docs/adr/0006-metrics-behind-the-narrow-interface.md.
const (
	opSelect   = "postgres.select"
	opGet      = "postgres.get"
	opExec     = "postgres.exec"
	opBeginTx  = "postgres.begin_tx"
	opCommitTx = "postgres.commit_tx"
)

// record reports one completed operation to the configured sink. A nil sink
// is the default, so this is a no-op unless the caller set one.
//
// A panicking sink must not take down the caller's operation, which has
// already completed by the time we get here.
func (c *Component) record(op string, d time.Duration, err error) {
	sink := c.cfg.onOperation()
	if sink == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("postgres: metrics sink panicked", "op", op, "panic", r)
		}
	}()
	sink(op, d, err)
}

// observe runs one [DB] operation against the live pool, times it, and reports
// the result. It also carries the not-ready check that every operation needs:
// with no live pool the operation is not attempted, and is reported with a
// zero duration and [ErrNotReady].
//
// fn returns this module's own error — [ErrNoRows] or a wrapped driver error —
// not the raw one, so the sink sees a stable vocabulary. Timing covers the
// driver call only.
func observe[T any](c *Component, op string, fn func(*pgxpool.Pool) (T, error)) (T, error) {
	var zero T
	pool := c.getPool()
	if pool == nil {
		c.record(op, 0, ErrNotReady)
		return zero, ErrNotReady
	}
	start := time.Now()
	v, err := fn(pool)
	c.record(op, time.Since(start), err)
	return v, err
}

// observeErr is [observe] for the operations that return no value.
func observeErr(c *Component, op string, fn func(*pgxpool.Pool) error) error {
	_, err := observe(c, op, func(pool *pgxpool.Pool) (struct{}, error) {
		return struct{}{}, fn(pool)
	})
	return err
}

// observeTx times an operation that acts on an open transaction rather than on
// the pool, so it has no handle to fetch.
func observeTx(c *Component, op string, fn func() error) error {
	start := time.Now()
	err := fn()
	c.record(op, time.Since(start), err)
	return err
}
