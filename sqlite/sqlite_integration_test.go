//go:build integration

// These tests exercise behaviour that an in-memory database cannot reach: WAL
// journaling, real write contention, directory creation, and persistence across
// component restarts. They need no docker-compose services — only a temp dir.
package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

// startFile starts a component on path and returns it plus its stop function.
func startFile(t *testing.T, cfg Config) (*Component, func()) {
	t.Helper()

	c := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	errCh := make(chan error, 1)

	go func() { errCh <- c.Start(ctx, func() { close(ready) }) }()

	select {
	case <-ready:
	case err := <-errCh:
		cancel()
		t.Fatalf("Start returned before ready: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("timed out waiting for ready()")
	}

	return c, func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := c.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start returned %v, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Start did not return after Stop")
		}
		cancel()
	}
}

// WAL must actually engage on a real file — the component treats a silent
// fallback as a startup failure, so this asserts the success side of that check.
func TestIntegrationWALEngaged(t *testing.T) {
	c, stop := startFile(t, Config{Path: tempDB(t)})
	defer stop()

	var mode string
	if err := c.Get(context.Background(), &mode, "PRAGMA journal_mode"); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want \"wal\"", mode)
	}
}

func TestIntegrationCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "app.db")

	_, stop := startFile(t, Config{Path: path})
	defer stop()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file not created at %s: %v", path, err)
	}
}

func TestIntegrationStartFailsOnUnwritablePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil { // read+execute, no write
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	c := New(Config{Path: filepath.Join(dir, "sub", "app.db")})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	readyCalled := false
	err := c.Start(ctx, func() { readyCalled = true })
	if err == nil {
		t.Error("Start = nil on an unwritable path, want error")
	}
	if readyCalled {
		t.Error("ready() was called despite Start failing")
	}
}

// Data must survive a Stop/Start cycle — the supervisor restarts components in
// place, and a restart that silently lost the database would be catastrophic.
func TestIntegrationPersistsAcrossRestart(t *testing.T) {
	path := tempDB(t)
	ctx := context.Background()

	c, stop := startFile(t, Config{Path: path})
	if _, err := c.Exec(ctx, `CREATE TABLE widget (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := c.Exec(ctx, `INSERT INTO widget (name) VALUES (?)`, "persisted"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	stop()

	c2, stop2 := startFile(t, Config{Path: path})
	defer stop2()

	var name string
	if err := c2.Get(ctx, &name, `SELECT name FROM widget WHERE id = 1`); err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if name != "persisted" {
		t.Errorf("name = %q after restart, want \"persisted\"", name)
	}
}

// With the default single-connection pool, concurrent writers queue inside
// database/sql instead of racing for the SQLite write lock, so none of them
// should see SQLITE_BUSY.
func TestIntegrationConcurrentWritersSerialise(t *testing.T) {
	c, stop := startFile(t, Config{Path: tempDB(t)})
	defer stop()

	ctx := context.Background()
	if _, err := c.Exec(ctx, `CREATE TABLE counter (id INTEGER PRIMARY KEY, n INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	const writers, perWriter = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				_, err := c.Exec(ctx, `INSERT INTO counter (n) VALUES (?)`, w*perWriter+i)
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	var count int
	if err := c.Get(ctx, &count, `SELECT COUNT(*) FROM counter`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if want := writers * perWriter; count != want {
		t.Errorf("row count = %d, want %d", count, want)
	}
}

// Concurrent readers must not block each other under WAL. This uses a larger
// pool, which is the configuration the MaxOpenConns docs describe as read-heavy.
func TestIntegrationConcurrentReadersWithLargerPool(t *testing.T) {
	c, stop := startFile(t, Config{Path: tempDB(t), MaxOpenConns: 4})
	defer stop()

	ctx := context.Background()
	if _, err := c.Exec(ctx, `CREATE TABLE widget (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := range 50 {
		if _, err := c.Exec(ctx, `INSERT INTO widget (name) VALUES (?)`, fmt.Sprintf("w%d", i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var names []string
			if err := c.Select(ctx, &names, `SELECT name FROM widget ORDER BY id`); err != nil {
				errs <- err
				return
			}
			if len(names) != 50 {
				errs <- fmt.Errorf("read %d rows, want 50", len(names))
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent read failed: %v", err)
	}
}

// A rolled-back transaction must leave no trace on a real file.
func TestIntegrationTransactionRollbackOnDisk(t *testing.T) {
	path := tempDB(t)
	ctx := context.Background()

	c, stop := startFile(t, Config{Path: path})
	if _, err := c.Exec(ctx, `CREATE TABLE widget (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id) VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	cause := errors.New("abort")
	if err := c.CommitTx(tx, cause); !errors.Is(err, cause) {
		t.Fatalf("CommitTx = %v, want the cause", err)
	}
	stop()

	// Reopen to confirm the rollback reached the file, not just the connection.
	c2, stop2 := startFile(t, Config{Path: path})
	defer stop2()

	var count int
	if err := c2.Get(ctx, &count, `SELECT COUNT(*) FROM widget`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("row count = %d after rollback, want 0", count)
	}
}

// Health must fail once the component is stopped even though a pooled
// connection previously succeeded.
func TestIntegrationHealthAfterStop(t *testing.T) {
	c, stop := startFile(t, Config{Path: tempDB(t)})

	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health while running: %v", err)
	}
	stop()
	if err := c.Health(context.Background()); err == nil {
		t.Error("Health = nil after Stop, want error")
	}
}
