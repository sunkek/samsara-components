# postgresql

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/postgresql.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/postgresql)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/postgresql)](https://goreportcard.com/report/github.com/sunkek/samsara-components/postgresql)

A [samsara](https://github.com/sunkek/samsara)-compatible PostgreSQL component
backed by [pgx/v5](https://github.com/jackc/pgx) connection pooling.

```
go get github.com/sunkek/samsara-components/postgresql
```

---

## Usage

### Register with a supervisor

```go
db := postgresql.New(postgresql.Config{
    Host: "localhost",
    Port: 5432,
    Name: "mydb",
    User: "myuser",
    Pass: "secret",
})
sup.Add(db,
    samsara.WithTier(samsara.TierCritical),
    samsara.WithRestartPolicy(samsara.ExponentialBackoff(5, time.Second)),
)
```

Or supply a full DSN:

```go
db := postgresql.New(postgresql.Config{
    URI: "postgres://user:pass@host:5432/db?sslmode=require",
})
```

### Use in domain adapters

Depend on the `DB` interface, not `*Component`, so adapters stay testable
without a real database:

```go
type UserRepo struct {
    db postgresql.DB
}

func (r *UserRepo) FindByID(ctx context.Context, id uuid.UUID) (*User, error) {
    var u User
    err := r.db.Get(ctx, &u, `SELECT * FROM users WHERE id = $1`, id)
    if errors.Is(err, postgresql.ErrNoRows) {
        return nil, ErrNotFound
    }
    return &u, err
}
```

### Transactions

```go
func (r *UserRepo) Transfer(ctx context.Context, from, to uuid.UUID, amount int) (err error) {
    tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return err
    }
    defer func() { err = r.db.CommitTx(ctx, tx, err) }()

    if _, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, from); err != nil {
        return err
    }
    if _, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, to); err != nil {
        return err
    }
    return nil
}
```

`CommitTx` commits when `err` is nil and rolls back when it is non-nil,
preserving both errors in the chain so callers can use `errors.Is`.

---

## Configuration

```go
postgresql.Config{
    // Individual fields (all have sensible defaults)
    Host    string        // default: "localhost"
    Port    int           // default: 5432
    Name    string        // default: "postgres"
    User    string        // default: "postgres"
    Pass    string
    SSLMode string        // default: "disable"

    // DSN override — takes precedence over individual fields when non-empty
    URI string

    // Timeouts and pool sizing
    ConnectTimeout time.Duration // default: 10s — startup ping deadline only
    MaxConns       int32         // default: pgx default (min(4, GOMAXPROCS))
    MinConns       int32         // default: 0
}
```

### Options

```go
postgresql.WithLogger(slog.Default())           // attach a structured logger
postgresql.WithName("postgres-replica")         // override component name
```

---

## API reference

### `DB` interface

| Method | Description |
|--------|-------------|
| `Select(ctx, &slice, sql, args...)` | Scan all rows into a slice of structs |
| `Get(ctx, &struct, sql, args...)` | Scan one row; returns `ErrNoRows` if empty |
| `Exec(ctx, sql, args...)` | Execute without scanning; returns command tag |
| `BeginTx(ctx, pgx.TxOptions{})` | Start a transaction |
| `CommitTx(ctx, tx, err)` | Commit or roll back; use in a defer |

`*Component` satisfies `DB`. Struct fields are mapped by the `db` tag.

### Sentinel errors

```go
errors.Is(err, postgresql.ErrNoRows) // no rows matched the query
```

---

## Health checking

`*Component` implements `samsara.HealthChecker` — the supervisor polls
`Health(ctx)` every health interval and flips `/readyz` accordingly.
No configuration required.

---

## Multiple instances (primary + replica)

```go
primary := postgresql.New(cfg.Primary, postgresql.WithName("postgres-primary"))
replica := postgresql.New(cfg.Replica, postgresql.WithName("postgres-replica"))

sup.Add(primary, samsara.WithTier(samsara.TierCritical))
sup.Add(replica, samsara.WithTier(samsara.TierSignificant))
```

---

## Testing adapters without a database

```go
type mockDB struct{ postgresql.DB }

func (m *mockDB) Get(_ context.Context, dst any, _ string, _ ...any) error {
    *dst.(*User) = User{ID: uuid.New(), Name: "test"}
    return nil
}
```

Or use the `TxFinaliser` interface to stub transactions:

```go
type fakeTx struct{}
func (f *fakeTx) Commit(_ context.Context) error   { return nil }
func (f *fakeTx) Rollback(_ context.Context) error { return nil }
```
