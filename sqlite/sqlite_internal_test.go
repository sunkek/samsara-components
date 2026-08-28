package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// startComponent starts c in a goroutine and returns a function that stops it
// and asserts Start returned nil. It fails the test if ready() is not called
// within the timeout.
func startComponent(t *testing.T, c *Component) (stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	errCh := make(chan error, 1)

	go func() { errCh <- c.Start(ctx, func() { close(ready) }) }()

	select {
	case <-ready:
	case err := <-errCh:
		cancel()
		t.Fatalf("Start returned before ready: %v", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("timed out waiting for ready()")
	}

	return func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := c.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start returned %v, want nil after clean stop", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Start did not return after Stop")
		}
		cancel()
	}
}

func TestConfigDefaults(t *testing.T) {
	var c Config

	if got := c.path(); got != MemoryPath {
		t.Errorf("path() = %q, want %q", got, MemoryPath)
	}
	if got := c.journalMode(); got != JournalWAL {
		t.Errorf("journalMode() = %q, want %q", got, JournalWAL)
	}
	if got := c.synchronous(); got != SyncNormal {
		t.Errorf("synchronous() = %q, want %q", got, SyncNormal)
	}
	if got := c.busyTimeout(); got != 5*time.Second {
		t.Errorf("busyTimeout() = %v, want 5s", got)
	}
	if got := c.connectTimeout(); got != 10*time.Second {
		t.Errorf("connectTimeout() = %v, want 10s", got)
	}
	if got := c.maxOpenConns(); got != 1 {
		t.Errorf("maxOpenConns() = %d, want 1", got)
	}
	if got := c.foreignKeys(); got != "on" {
		t.Errorf("foreignKeys() = %q, want \"on\"", got)
	}
}

func TestConfigOverrides(t *testing.T) {
	c := Config{
		Path:               "/tmp/x.db",
		JournalMode:        "delete", // lowercase must normalise
		Synchronous:        "full",
		BusyTimeout:        time.Second,
		ConnectTimeout:     2 * time.Second,
		MaxOpenConns:       4,
		DisableForeignKeys: true,
	}

	if got := c.journalMode(); got != JournalDelete {
		t.Errorf("journalMode() = %q, want %q", got, JournalDelete)
	}
	if got := c.synchronous(); got != SyncFull {
		t.Errorf("synchronous() = %q, want %q", got, SyncFull)
	}
	if got := c.maxOpenConns(); got != 4 {
		t.Errorf("maxOpenConns() = %d, want 4", got)
	}
	if got := c.foreignKeys(); got != "off" {
		t.Errorf("foreignKeys() = %q, want \"off\"", got)
	}
}

// An in-memory database is private per connection, so a pool larger than one
// would hand out connections to different databases.
func TestConfigInMemoryPinsPoolToOne(t *testing.T) {
	c := Config{Path: MemoryPath, MaxOpenConns: 8}
	if got := c.maxOpenConns(); got != 1 {
		t.Errorf("maxOpenConns() = %d, want 1 for in-memory", got)
	}

	shared := Config{Path: "file:x?mode=memory&cache=shared", MaxOpenConns: 8}
	if !shared.inMemory() {
		t.Error("inMemory() = false for a mode=memory path")
	}
}

func TestConfigDSN(t *testing.T) {
	c := Config{Path: "/var/lib/app.db", BusyTimeout: 3 * time.Second}
	dsn := c.dsn()

	prefix := "file:/var/lib/app.db?"
	if !strings.HasPrefix(dsn, prefix) {
		t.Fatalf("dsn() = %q, want prefix %q", dsn, prefix)
	}

	q, err := url.ParseQuery(strings.TrimPrefix(dsn, prefix))
	if err != nil {
		t.Fatalf("dsn() produced an unparseable query: %v", err)
	}

	got := q["_pragma"]
	want := map[string]bool{
		"busy_timeout(3000)":  false,
		"journal_mode(WAL)":   false,
		"synchronous(NORMAL)": false,
		"foreign_keys(on)":    false,
	}
	for _, p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected pragma %q", p)
			continue
		}
		want[p] = true
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("missing pragma %q in %v", p, got)
		}
	}
}

func TestNameDefaultAndOverride(t *testing.T) {
	if got := New(Config{}).Name(); got != "sqlite" {
		t.Errorf("Name() = %q, want \"sqlite\"", got)
	}
	if got := New(Config{}, WithName("cache")).Name(); got != "cache" {
		t.Errorf("Name() = %q, want \"cache\"", got)
	}
}

func TestStartCallsReadyExactlyOnce(t *testing.T) {
	c := New(Config{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	calls := 0
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Start(ctx, func() {
			mu.Lock()
			calls++
			mu.Unlock()
		})
	}()

	// Give Start time to complete verification and settle into its blocking select.
	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("ready() was never called")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := c.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Errorf("Start returned %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("ready() called %d times, want exactly 1", calls)
	}
}

// The supervisor treats a non-nil return as a crash, so a cancelled context
// must produce nil rather than ctx.Err().
func TestStartReturnsNilOnContextCancel(t *testing.T) {
	c := New(Config{})

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	errCh := make(chan error, 1)
	go func() { errCh <- c.Start(ctx, func() { close(ready) }) }()

	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("timed out waiting for ready()")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned %v, want nil on context cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	c := New(Config{})
	stop := startComponent(t, c)
	stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := range 3 {
		if err := c.Stop(ctx); err != nil {
			t.Errorf("Stop call %d: %v", i+2, err)
		}
	}
}

func TestStopBeforeStartIsSafe(t *testing.T) {
	c := New(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Errorf("Stop before Start: %v", err)
	}
}

// Stop may be called concurrently with a still-initialising Start.
func TestConcurrentStopIsSafe(t *testing.T) {
	c := New(Config{})
	stop := startComponent(t, c)
	defer stop()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Stop(ctx); err != nil {
				t.Errorf("concurrent Stop: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestHealthBeforeStartAndAfterStop(t *testing.T) {
	c := New(Config{})

	ctx := context.Background()
	if err := c.Health(ctx); err == nil {
		t.Error("Health() = nil before Start, want error")
	}

	stop := startComponent(t, c)
	if err := c.Health(ctx); err != nil {
		t.Errorf("Health() = %v while running, want nil", err)
	}

	stop()
	if err := c.Health(ctx); err == nil {
		t.Error("Health() = nil after Stop, want error")
	}
}

// A restarted component must be fully usable again — the supervisor calls
// Start after Stop when a restart policy fires.
func TestRestartCycle(t *testing.T) {
	c := New(Config{})

	for i := range 3 {
		stop := startComponent(t, c)
		if err := c.Health(context.Background()); err != nil {
			t.Fatalf("cycle %d: Health after restart: %v", i, err)
		}
		stop()
	}
}

func TestQueriesBeforeStartReturnError(t *testing.T) {
	c := New(Config{})
	ctx := context.Background()

	var dst []int
	if err := c.Select(ctx, &dst, "SELECT 1"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Select before Start = %v, want ErrNotReady", err)
	}
	var one int
	if err := c.Get(ctx, &one, "SELECT 1"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Get before Start = %v, want ErrNotReady", err)
	}
	if _, err := c.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Exec before Start = %v, want ErrNotReady", err)
	}
	if _, err := c.BeginTx(ctx, nil); !errors.Is(err, ErrNotReady) {
		t.Errorf("BeginTx before Start = %v, want ErrNotReady", err)
	}
}

func TestSelectGetExec(t *testing.T) {
	c := New(Config{})
	defer startComponent(t, c)()

	ctx := context.Background()

	if _, err := c.Exec(ctx, `CREATE TABLE widget (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	res, err := c.Exec(ctx, `INSERT INTO widget (name) VALUES (?), (?)`, "alpha", "beta")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 2 {
		t.Errorf("RowsAffected() = %d, %v; want 2, nil", n, err)
	}

	type widget struct {
		ID   int    `db:"id"`
		Name string `db:"name"`
	}

	var all []widget
	if err := c.Select(ctx, &all, `SELECT id, name FROM widget ORDER BY id`); err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(all) != 2 || all[0].Name != "alpha" || all[1].Name != "beta" {
		t.Errorf("Select returned %+v, want alpha then beta", all)
	}

	var one widget
	if err := c.Get(ctx, &one, `SELECT id, name FROM widget WHERE name = ?`, "beta"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if one.Name != "beta" {
		t.Errorf("Get returned %+v, want beta", one)
	}
}

// An empty result set is an error for Get but not for Select.
func TestGetNoRowsSelectEmpty(t *testing.T) {
	c := New(Config{})
	defer startComponent(t, c)()

	ctx := context.Background()
	if _, err := c.Exec(ctx, `CREATE TABLE widget (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	var one int
	err := c.Get(ctx, &one, `SELECT id FROM widget WHERE id = 999`)
	if !errors.Is(err, ErrNoRows) {
		t.Errorf("Get on empty result = %v, want ErrNoRows", err)
	}

	var all []int
	if err := c.Select(ctx, &all, `SELECT id FROM widget`); err != nil {
		t.Errorf("Select on empty table = %v, want nil", err)
	}
	if len(all) != 0 {
		t.Errorf("Select returned %d rows, want 0", len(all))
	}
}

// SQLite defaults foreign_keys to OFF; the component defaults it to ON, and the
// pragma must apply to every pooled connection rather than only the first.
func TestForeignKeysEnabledByDefault(t *testing.T) {
	c := New(Config{})
	defer startComponent(t, c)()

	ctx := context.Background()
	if _, err := c.Exec(ctx, `CREATE TABLE parent (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := c.Exec(ctx, `CREATE TABLE child (
		id INTEGER PRIMARY KEY,
		parent_id INTEGER NOT NULL REFERENCES parent(id)
	)`); err != nil {
		t.Fatalf("create child: %v", err)
	}

	if _, err := c.Exec(ctx, `INSERT INTO child (parent_id) VALUES (42)`); err == nil {
		t.Error("insert violating a foreign key succeeded, want constraint failure")
	}
}

func TestForeignKeysDisabled(t *testing.T) {
	c := New(Config{DisableForeignKeys: true})
	defer startComponent(t, c)()

	ctx := context.Background()
	if _, err := c.Exec(ctx, `CREATE TABLE parent (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := c.Exec(ctx, `CREATE TABLE child (
		id INTEGER PRIMARY KEY,
		parent_id INTEGER NOT NULL REFERENCES parent(id)
	)`); err != nil {
		t.Fatalf("create child: %v", err)
	}

	if _, err := c.Exec(ctx, `INSERT INTO child (parent_id) VALUES (42)`); err != nil {
		t.Errorf("insert with foreign keys disabled = %v, want nil", err)
	}
}

func TestTransactionCommit(t *testing.T) {
	c := New(Config{})
	defer startComponent(t, c)()

	ctx := context.Background()
	if _, err := c.Exec(ctx, `CREATE TABLE widget (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id) VALUES (1)`); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	if err := c.CommitTx(tx, nil); err != nil {
		t.Fatalf("CommitTx: %v", err)
	}

	var count int
	if err := c.Get(ctx, &count, `SELECT COUNT(*) FROM widget`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("row count after commit = %d, want 1", count)
	}
}

func TestTransactionRollbackPreservesCause(t *testing.T) {
	c := New(Config{})
	defer startComponent(t, c)()

	ctx := context.Background()
	if _, err := c.Exec(ctx, `CREATE TABLE widget (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	cause := errors.New("domain failure")

	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id) VALUES (1)`); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	if err := c.CommitTx(tx, cause); !errors.Is(err, cause) {
		t.Errorf("CommitTx returned %v, want the original cause", err)
	}

	var count int
	if err := c.Get(ctx, &count, `SELECT COUNT(*) FROM widget`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("row count after rollback = %d, want 0", count)
	}
}

// stubTx lets the rollback-failure path be exercised without a real database.
type stubTx struct {
	commitErr   error
	rollbackErr error
}

func (s stubTx) Commit() error   { return s.commitErr }
func (s stubTx) Rollback() error { return s.rollbackErr }

func TestCommitTxRollbackFailureWrapsBothErrors(t *testing.T) {
	c := New(Config{})

	cause := errors.New("domain failure")
	rbErr := errors.New("rollback exploded")

	err := c.CommitTx(stubTx{rollbackErr: rbErr}, cause)
	if !errors.Is(err, cause) {
		t.Errorf("error chain lost the cause: %v", err)
	}
	if !errors.Is(err, rbErr) {
		t.Errorf("error chain lost the rollback error: %v", err)
	}
}

// A transaction already finalised by its context reports ErrTxDone on rollback;
// that is expected cleanup noise, not a rollback failure worth reporting.
func TestCommitTxIgnoresErrTxDoneOnRollback(t *testing.T) {
	c := New(Config{})

	cause := errors.New("domain failure")
	err := c.CommitTx(stubTx{rollbackErr: sql.ErrTxDone}, cause)
	if err != cause {
		t.Errorf("CommitTx = %v, want the cause unwrapped", err)
	}
}

func TestCommitTxPropagatesCommitError(t *testing.T) {
	c := New(Config{})

	commitErr := errors.New("commit exploded")
	err := c.CommitTx(stubTx{commitErr: commitErr}, nil)
	if !errors.Is(err, commitErr) {
		t.Errorf("CommitTx = %v, want the commit error", err)
	}
}

// ----------------------------------------------------------------------------
// Config
// ----------------------------------------------------------------------------

// A zero-value Config must produce a usable component: every default is
// supplied by an unexported accessor at the point of use, so New never needs a
// populated Config.
func TestConfig_ZeroValueNoPanic(t *testing.T) {
	c := New(Config{})
	if c == nil {
		t.Fatal("expected non-nil component")
	}
	if c.Name() == "" {
		t.Error("expected a default name")
	}
}
