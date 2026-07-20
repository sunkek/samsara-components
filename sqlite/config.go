package sqlite

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Journal modes accepted by [Config.JournalMode].
const (
	JournalWAL      = "WAL"
	JournalDelete   = "DELETE"
	JournalTruncate = "TRUNCATE"
	JournalPersist  = "PERSIST"
	JournalMemory   = "MEMORY"
	JournalOff      = "OFF"
)

// Synchronous levels accepted by [Config.Synchronous].
const (
	SyncOff    = "OFF"
	SyncNormal = "NORMAL"
	SyncFull   = "FULL"
	SyncExtra  = "EXTRA"
)

// MemoryPath opens a private in-memory database. Each connection to a plain
// ":memory:" database gets its own private copy, so a pool of more than one
// connection would see divergent data; [Config.maxOpenConns] therefore pins
// in-memory databases to a single connection regardless of MaxOpenConns.
const MemoryPath = ":memory:"

// Config holds all parameters for the SQLite component.
//
// The zero value is usable and opens an in-memory database with WAL journaling
// and a single connection — suitable for tests. Production callers should at
// minimum set Path.
type Config struct {
	// Path is the database file path. Defaults to [MemoryPath].
	// Parent directories are created during Start if they do not exist.
	Path string

	// BusyTimeout is how long a blocked writer waits for the lock before
	// returning SQLITE_BUSY. Defaults to 5 s.
	BusyTimeout time.Duration

	// JournalMode sets PRAGMA journal_mode. Defaults to [JournalWAL].
	// WAL is strongly recommended: it is the only mode that lets readers
	// proceed concurrently with a writer.
	JournalMode string

	// Synchronous sets PRAGMA synchronous. Defaults to [SyncNormal], which is
	// the safe pairing with WAL — durable against application crashes, and
	// against power loss up to the last checkpoint.
	Synchronous string

	// ForeignKeys enables PRAGMA foreign_keys. SQLite defaults this to OFF for
	// backwards compatibility; this component defaults it to ON. Set
	// DisableForeignKeys to opt out.
	DisableForeignKeys bool

	// MaxOpenConns caps the connection pool. Defaults to 1.
	//
	// SQLite permits exactly one writer at a time. With the default of 1, write
	// contention is resolved inside the pool by goroutines queueing for the
	// single connection, and SQLITE_BUSY cannot occur between them. Raising this
	// moves that contention into SQLite itself, where it is resolved by
	// BusyTimeout and can still fail with SQLITE_BUSY under sustained load —
	// so raise it only with JournalWAL set, and only if reads dominate.
	MaxOpenConns int

	// ConnectTimeout bounds the open + verification round-trips during Start.
	// Defaults to 10 s.
	ConnectTimeout time.Duration
}

func (c Config) path() string {
	if c.Path == "" {
		return MemoryPath
	}
	return c.Path
}

// inMemory reports whether the configured path is an in-memory database.
func (c Config) inMemory() bool {
	p := c.path()
	return p == MemoryPath || strings.Contains(p, "mode=memory")
}

func (c Config) journalMode() string {
	if c.JournalMode == "" {
		return JournalWAL
	}
	return strings.ToUpper(c.JournalMode)
}

func (c Config) synchronous() string {
	if c.Synchronous == "" {
		return SyncNormal
	}
	return strings.ToUpper(c.Synchronous)
}

func (c Config) busyTimeout() time.Duration {
	if c.BusyTimeout > 0 {
		return c.BusyTimeout
	}
	return 5 * time.Second
}

func (c Config) connectTimeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return 10 * time.Second
}

func (c Config) maxOpenConns() int {
	// An in-memory database is private per connection, so a larger pool would
	// hand out connections to different databases. Pin to 1 regardless.
	if c.inMemory() {
		return 1
	}
	if c.MaxOpenConns > 0 {
		return c.MaxOpenConns
	}
	return 1
}

func (c Config) foreignKeys() string {
	if c.DisableForeignKeys {
		return "off"
	}
	return "on"
}

// dsn composes the modernc.org/sqlite connection string. Pragmas are passed as
// _pragma query parameters so they are applied to every connection the pool
// opens, not just the first — a per-connection setting like foreign_keys is
// silently lost otherwise.
func (c Config) dsn() string {
	pragmas := []string{
		"busy_timeout(" + fmt.Sprint(c.busyTimeout().Milliseconds()) + ")",
		"journal_mode(" + c.journalMode() + ")",
		"synchronous(" + c.synchronous() + ")",
		"foreign_keys(" + c.foreignKeys() + ")",
	}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	return "file:" + c.path() + "?" + q.Encode()
}
