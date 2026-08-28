package sqlite

import (
	"database/sql"
	"time"
)

// Operation names reported to [Config.OnOperation]. They are fixed per method
// and never derived from a query string, which is unbounded and would blow up
// label cardinality in the sink.
// See docs/adr/0006-metrics-behind-the-narrow-interface.md.
const (
	opSelect   = "sqlite.select"
	opGet      = "sqlite.get"
	opExec     = "sqlite.exec"
	opBeginTx  = "sqlite.begin_tx"
	opCommitTx = "sqlite.commit_tx"
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
			c.log.Error("sqlite: metrics sink panicked", "op", op, "panic", r)
		}
	}()
	sink(op, d, err)
}

// observe runs one [DB] operation against the live handle, times it, and
// reports the result. It also carries the not-started check that every
// operation needs: with no open database the operation is not attempted, and
// is reported with a zero duration.
//
// fn returns this module's own error — [ErrNoRows] or a wrapped driver error —
// not the raw one, so the sink sees a stable vocabulary. Timing covers the
// driver call only.
func observe[T any](c *Component, op string, fn func(*sql.DB) (T, error)) (T, error) {
	var zero T
	db := c.getDB()
	if db == nil {
		c.record(op, 0, errNotStarted)
		return zero, errNotStarted
	}
	start := time.Now()
	v, err := fn(db)
	c.record(op, time.Since(start), err)
	return v, err
}

// observeErr is [observe] for the operations that return no value.
func observeErr(c *Component, op string, fn func(*sql.DB) error) error {
	_, err := observe(c, op, func(db *sql.DB) (struct{}, error) {
		return struct{}{}, fn(db)
	})
	return err
}

// observeTx times an operation that acts on an open transaction rather than on
// the pool, so it has no handle to fetch and no not-started path.
func observeTx(c *Component, op string, fn func() error) error {
	start := time.Now()
	err := fn()
	c.record(op, time.Since(start), err)
	return err
}
