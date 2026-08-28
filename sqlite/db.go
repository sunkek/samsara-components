package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/georgysavva/scany/v2/sqlscan"
)

// DB is the interface that domain adapters should depend on.
// *Component satisfies it; depend on DB rather than *Component to keep
// adapters testable.
//
//	type TargetRepo struct { db sqlite.DB }
type DB interface {
	// Select executes query and scans all result rows into dst (a pointer to a
	// slice). An empty result set is not an error; dst is left empty.
	Select(ctx context.Context, dst any, query string, args ...any) error

	// Get executes query and scans the first result row into dst (a pointer to
	// a struct or scalar). Returns [ErrNoRows] if no row was found.
	Get(ctx context.Context, dst any, query string, args ...any) error

	// Exec executes query and returns the result. Use for INSERT/UPDATE/DELETE
	// and DDL, where no rows need scanning.
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

	// BeginTx starts a transaction with the given options.
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)

	// CommitTx commits tx if inErr is nil, and rolls back if inErr is non-nil.
	// Use in a defer to guarantee finalisation:
	//
	//	tx, err := db.BeginTx(ctx, nil)
	//	if err != nil { return err }
	//	defer func() { err = db.CommitTx(tx, err) }()
	CommitTx(tx TxFinaliser, inErr error) error
}

// TxFinaliser is the minimal transaction interface required by [Component.CommitTx].
// *sql.Tx satisfies it. Define a local stub in tests to avoid a real database.
//
// Unlike the pgx equivalent, these methods take no context: database/sql binds
// the transaction's context at BeginTx and Commit/Rollback inherit it.
type TxFinaliser interface {
	Commit() error
	Rollback() error
}

// ErrNoRows is returned by [Component.Get] when no row matches the query.
// Use errors.Is(err, sqlite.ErrNoRows) to check.
var ErrNoRows = sql.ErrNoRows

// ErrNotReady is returned by every [DB] operation when the component has no
// open database: before [Component.Start] succeeds, after [Component.Stop], or
// while the supervisor is restarting it. Callers get this error instead of a
// nil-pointer panic and can choose to fail open. Use
// errors.Is(err, sqlite.ErrNotReady) to check.
//
// It is named to match redis.ErrNotReady and postgresql.ErrNotReady, so the
// same check reads the same across components.
var ErrNotReady = errors.New("sqlite: not initialised (component not started)")

// Compile-time assertion that *Component satisfies its own DB interface.
var _ DB = (*Component)(nil)

// Select executes query and scans all result rows into dst.
// dst must be a pointer to a slice of structs or scalars.
func (c *Component) Select(ctx context.Context, dst any, query string, args ...any) error {
	return observeErr(c, opSelect, func(db *sql.DB) error {
		if err := sqlscan.Select(ctx, db, dst, query, args...); err != nil {
			return fmt.Errorf("sqlite select: %w", err)
		}
		return nil
	})
}

// Get executes query and scans the first result row into dst.
// dst must be a pointer to a struct or scalar type.
func (c *Component) Get(ctx context.Context, dst any, query string, args ...any) error {
	return observeErr(c, opGet, func(db *sql.DB) error {
		if err := sqlscan.Get(ctx, db, dst, query, args...); err != nil {
			// sqlscan reports an empty result as its own sentinel; translate it
			// so callers can use the single errors.Is(err, ErrNoRows) check that
			// works across both this component and the postgresql one.
			if sqlscan.NotFound(err) {
				return fmt.Errorf("sqlite get: %w", ErrNoRows)
			}
			return fmt.Errorf("sqlite get: %w", err)
		}
		return nil
	})
}

// Exec executes query without scanning rows.
// Useful for INSERT, UPDATE, DELETE, DDL.
func (c *Component) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return observe(c, opExec, func(db *sql.DB) (sql.Result, error) {
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("sqlite exec: %w", err)
		}
		return res, nil
	})
}

// BeginTx starts a new transaction. Pass nil opts for the driver defaults.
func (c *Component) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return observe(c, opBeginTx, func(db *sql.DB) (*sql.Tx, error) {
		tx, err := db.BeginTx(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("sqlite begin tx: %w", err)
		}
		return tx, nil
	})
}

// CommitTx commits tx when inErr is nil, and rolls back when inErr is non-nil.
// Both errors are preserved in the returned chain so callers can use errors.Is.
//
// On the rollback path the error reported to [Config.OnOperation] is the
// caller's inErr, which describes why the transaction was abandoned rather
// than a failure of the rollback itself.
func (c *Component) CommitTx(tx TxFinaliser, inErr error) error {
	return observeTx(c, opCommitTx, func() error {
		if inErr != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				c.log.Error("sqlite: rollback failed", "error", rbErr, "cause", inErr)
				return fmt.Errorf("sqlite rollback (%w) after: %w", rbErr, inErr)
			}
			return inErr
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite commit: %w", err)
		}
		return nil
	})
}
