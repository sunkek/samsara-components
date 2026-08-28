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

// observe runs one [DB] operation against the pool, times it, and reports the
// result.
//
// Unlike its redis and sqlite counterparts it carries no not-ready check:
// this component has never had one, and adding it here would turn a panic on
// an unstarted component into an error return — a behaviour change, which
// instrumentation may not make. Giving this component a not-ready sentinel is
// worth doing on its own merits, and separately.
//
// fn returns this module's own error — [ErrNoRows] or a wrapped driver error —
// not the raw one, so the sink sees a stable vocabulary.
func observe[T any](c *Component, op string, fn func(*pgxpool.Pool) (T, error)) (T, error) {
	start := time.Now()
	v, err := fn(c.getPool())
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
