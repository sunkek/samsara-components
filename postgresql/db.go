package postgresql

import (
	"context"
	"errors"
	"fmt"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the interface that domain adapters should depend on.
// *Component satisfies it; depend on DB rather than *Component to keep
// adapters testable.
//
//	type UserRepo struct { db postgresql.DB }
//
// Metrics reported to [Config.OnOperation] cover calls made through this
// interface only. Work done through [Component.Pool] is not measured.
type DB interface {
	// Select executes sql and scans all result rows into dst (a pointer to a
	// slice). Returns [ErrNoRows] if the result set is empty.
	Select(ctx context.Context, dst any, sql string, args ...any) error

	// Get executes sql and scans the first result row into dst (a pointer to a
	// struct or scalar). Returns [ErrNoRows] if no row was found.
	Get(ctx context.Context, dst any, sql string, args ...any) error

	// Exec executes sql and returns the command tag. Use for INSERT/UPDATE/DELETE
	// where you don't need to scan rows.
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)

	// BeginTx starts a transaction with the given options.
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)

	// CommitTx commits tx if inErr is nil, and rolls back if inErr is non-nil.
	// Use in a defer to guarantee finalisation:
	//
	//	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	//	if err != nil { return err }
	//	defer func() { err = db.CommitTx(ctx, tx, err) }()
	CommitTx(ctx context.Context, tx TxFinaliser, inErr error) error
}

// TxFinaliser is the minimal transaction interface required by [CommitTx].
// pgx.Tx satisfies it. Define a local stub in tests to avoid a real database.
type TxFinaliser interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Compile-time assertion: *Component satisfies DB.
var _ DB = (*Component)(nil)

// ErrNoRows is returned by [Select] and [Get] when no rows match the query.
// Use errors.Is(err, postgresql.ErrNoRows) to check.
var ErrNoRows = pgx.ErrNoRows

// ErrNotReady is returned by every [DB] operation that needs the pool when the
// component has no live connection: before [Component.Start] succeeds, after
// [Component.Stop], or while the supervisor is restarting it. Callers get this
// error instead of a nil-pointer panic and can choose to fail open. Use
// errors.Is(err, postgresql.ErrNotReady) to check.
//
// [CommitTx] acts on a transaction the caller already holds, so it does not
// return this.
//
// It is named to match redis.ErrNotReady and sqlite.ErrNotReady, so the same
// check reads the same across components.
var ErrNotReady = errors.New("postgres: pool not initialised (component not started)")

// Select executes sql and scans all result rows into dst.
// dst must be a pointer to a slice of structs or scalars.
func (c *Component) Select(ctx context.Context, dst any, sql string, args ...any) error {
	return observeErr(c, opSelect, func(pool *pgxpool.Pool) error {
		if err := pgxscan.Select(ctx, pool, dst, sql, args...); err != nil {
			return fmt.Errorf("postgres select: %w", err)
		}
		return nil
	})
}

// Get executes sql and scans the first result row into dst.
// dst must be a pointer to a struct or scalar type.
func (c *Component) Get(ctx context.Context, dst any, sql string, args ...any) error {
	return observeErr(c, opGet, func(pool *pgxpool.Pool) error {
		if err := pgxscan.Get(ctx, pool, dst, sql, args...); err != nil {
			return fmt.Errorf("postgres get: %w", err)
		}
		return nil
	})
}

// Exec executes sql without scanning rows.
// Useful for INSERT, UPDATE, DELETE, DDL.
func (c *Component) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return observe(c, opExec, func(pool *pgxpool.Pool) (pgconn.CommandTag, error) {
		tag, err := pool.Exec(ctx, sql, args...)
		if err != nil {
			return tag, fmt.Errorf("postgres exec: %w", err)
		}
		return tag, nil
	})
}

// BeginTx starts a new transaction.
func (c *Component) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return observe(c, opBeginTx, func(pool *pgxpool.Pool) (pgx.Tx, error) {
		tx, err := pool.BeginTx(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("postgres begin tx: %w", err)
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
func (c *Component) CommitTx(ctx context.Context, tx TxFinaliser, inErr error) error {
	return observeTx(c, opCommitTx, func() error {
		if inErr != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				c.log.Error("postgres: rollback failed", "error", rbErr, "cause", inErr)
				return fmt.Errorf("postgres rollback (%w) after: %w", rbErr, inErr)
			}
			return inErr
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres commit: %w", err)
		}
		return nil
	})
}
