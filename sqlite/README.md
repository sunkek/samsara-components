# sqlite

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/sqlite.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/sqlite)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/sqlite)](https://goreportcard.com/report/github.com/sunkek/samsara-components/sqlite)

A [samsara](https://github.com/sunkek/samsara)-compatible SQLite component
backed by [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — a pure-Go
driver, so services using this component still build with `CGO_ENABLED=0`.

```
go get github.com/sunkek/samsara-components/sqlite
```

---

## Usage

### Register with a supervisor

```go
db := sqlite.New(sqlite.Config{Path: "/var/lib/myapp/data.db"})
sup.Add(db,
    samsara.WithTier(samsara.TierCritical),
    samsara.WithRestartPolicy(samsara.ExponentialBackoff(5, time.Second)),
)
```

The zero-value config opens a private in-memory database, which is what you
want in tests:

```go
db := sqlite.New(sqlite.Config{})
```

### Use in domain adapters

Depend on the `DB` interface, not `*Component`, so adapters stay testable
without a real database:

```go
type TargetRepo struct {
    db sqlite.DB
}

func (r *TargetRepo) ByClient(ctx context.Context, clientID int64) ([]Target, error) {
    var out []Target
    err := r.db.Select(ctx, &out,
        `SELECT id, kind, value FROM targets WHERE client_id = ?`, clientID)
    return out, err
}
```

`Get` returns `ErrNoRows` when no row matches; `Select` treats an empty result
as success. Both use the same signatures as the `postgresql` component, so the
two are interchangeable at the adapter boundary.

### Transactions

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer func() { err = db.CommitTx(tx, err) }()

_, err = tx.ExecContext(ctx, `INSERT INTO widget (name) VALUES (?)`, name)
```

`CommitTx` commits when `inErr` is nil and rolls back otherwise, returning the
original error so `errors.Is` still works on it. Unlike the pgx equivalent it
takes no context — `database/sql` binds the transaction's context at `BeginTx`.

---

## Configuration

| Field | Default | Notes |
|---|---|---|
| `Path` | `:memory:` | Parent directories are created during `Start`. |
| `BusyTimeout` | 5s | How long a blocked writer waits before `SQLITE_BUSY`. |
| `JournalMode` | `WAL` | Verified at startup; see below. |
| `Synchronous` | `NORMAL` | The safe pairing with WAL. |
| `DisableForeignKeys` | `false` | Foreign keys are **on** by default, unlike bare SQLite. |
| `MaxOpenConns` | 1 | See concurrency below. |
| `ConnectTimeout` | 10s | Bounds the open + verification round-trips. |
| `OnOperation` | nil | Per-operation metrics sink; see [Metrics](#metrics). |

Pragmas are passed as `_pragma` DSN parameters so they apply to every connection
the pool opens. Setting a per-connection pragma like `foreign_keys` with a
one-off `Exec` after startup silently fails to cover later connections.

---

## Concurrency

SQLite permits exactly one writer at a time, so `MaxOpenConns` defaults to **1**.
With a single connection, concurrent writers queue inside `database/sql` and
cannot collide — `SQLITE_BUSY` is impossible between goroutines of the same
process.

Raising `MaxOpenConns` moves that contention into SQLite itself, where it is
resolved by `BusyTimeout` and can still fail under sustained write load. Raise it
only when reads dominate, and only with `JournalWAL` set (the default), since WAL
is the only journal mode where readers proceed concurrently with a writer.

An in-memory database is private per connection, so the pool is pinned to 1
regardless of `MaxOpenConns` — a larger pool would hand out connections to
different databases.

---

## Startup verification

`sql.Open` is lazy and never touches the filesystem, so `Start` does the work
that makes `ready()` meaningful:

1. Creates parent directories for `Path`.
2. `PingContext` — confirms the file is openable.
3. `PRAGMA journal_mode` — confirms the requested mode actually engaged.

Step 3 exists because SQLite **silently** falls back to a rollback journal when
WAL is unavailable, most commonly on network filesystems, which lack the
shared-memory primitive WAL requires. The component treats that fallback as a
startup failure. Discovering it at boot is much cheaper than discovering it as
unexplained `SQLITE_BUSY` errors under load in production.

`Health` runs `SELECT 1` rather than a ping, because `database/sql` can satisfy a
ping from a pooled connection whose underlying file has been deleted or replaced.

---

## Testing

```bash
go test -race -count=3 ./...                  # unit tests, in-memory
go test -race -count=1 -tags integration ./... # integration tests
```

Integration tests need no docker-compose services — they use `t.TempDir()` — and
cover WAL engagement, concurrent writers and readers, directory creation,
persistence across restarts, and rollback durability.

---

## Sentinel errors

```go
errors.Is(err, sqlite.ErrNotReady) // no open database; the call was not attempted
```

---

## Metrics

`Config.OnOperation` is called once per completed `DB` operation with a fixed
operation name, how long the call took, and the error it returned. It defaults to
nil, which disables reporting entirely.

```go
c := sqlite.New(sqlite.Config{
    OnOperation: func(op string, d time.Duration, err error) {
        // op is one of: sqlite.select, sqlite.get, sqlite.exec, sqlite.begin_tx, sqlite.commit_tx
        metrics.Observe(op, d, err)
    },
})
```

`ErrNotReady` is not an operation failure: it means there was no live handle and
the call was never attempted. Those are reported with a **zero duration**, so
they never enter the latency distribution. Classify accordingly before counting
error rates.

See [ADR-0006](../docs/adr/0006-metrics-behind-the-narrow-interface.md).
