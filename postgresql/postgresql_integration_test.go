//go:build integration

package postgresql_test

// Integration tests require a running PostgreSQL instance.
// Start one with: make infra-up (from the repo root).
//
// Run these tests with:
//   go test -race -tags integration -timeout 120s ./...
// or via the repo root:
//   make test-integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sunkek/samsara-components/postgresql"
)

// testDSN matches the docker-compose.yml credentials.
const testDSN = "postgres://test:test@localhost:5432/test?sslmode=disable"

func testComp(t *testing.T) *postgresql.Component {
	t.Helper()
	return postgresql.New(
		postgresql.Config{URI: testDSN, ConnectTimeout: 10 * time.Second},
		postgresql.WithLogger(&testLogger{t}),
	)
}

// startComp starts the component in a goroutine, waits for ready, and
// registers a Stop+wait cleanup on t.Cleanup.
func startComp(t *testing.T, comp *postgresql.Component) {
	t.Helper()
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		errCh <- comp.Start(ctx, func() { close(readyCh) })
	}()

	select {
	case <-readyCh:
	case err := <-errCh:
		cancel()
		t.Fatalf("Start failed: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("Start timed out")
	}

	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := comp.Stop(stopCtx); err != nil {
			t.Errorf("Stop returned error: %v", err)
		}
		// Drain the Start goroutine.
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start returned unexpected error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Start goroutine did not exit after Stop")
		}
	})
}

// TestIntegration_StartStop verifies the full Start→ready→Stop lifecycle.
func TestIntegration_StartStop(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	// If we reach here, ready() was called and the component is running.
}

// TestIntegration_Health verifies Health returns nil when the pool is live.
func TestIntegration_Health(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := comp.Health(ctx); err != nil {
		t.Fatalf("Health returned error on live connection: %v", err)
	}
}

// TestIntegration_Restart verifies Stop→Start→ready works a second time
// (exercises the stopCh reinitialisation path).
func TestIntegration_Restart(t *testing.T) {
	comp := testComp(t)

	for i := range 2 {
		t.Logf("run %d", i+1)
		readyCh := make(chan struct{})
		errCh := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())

		go func() { errCh <- comp.Start(ctx, func() { close(readyCh) }) }()

		select {
		case <-readyCh:
		case err := <-errCh:
			cancel()
			t.Fatalf("run %d: Start failed: %v", i+1, err)
		case <-time.After(15 * time.Second):
			cancel()
			t.Fatalf("run %d: Start timed out", i+1)
		}

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = comp.Stop(stopCtx)
		stopCancel()
		cancel()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("run %d: Start returned error: %v", i+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("run %d: Start goroutine did not exit", i+1)
		}
	}
}

// TestIntegration_Exec verifies Exec runs DDL and DML without error.
func TestIntegration_Exec(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	_, err := comp.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS _sc_exec_test (id SERIAL PRIMARY KEY, val TEXT)`)
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	t.Cleanup(func() {
		_, _ = comp.Exec(context.Background(), `DROP TABLE IF EXISTS _sc_exec_test`)
	})

	tag, err := comp.Exec(ctx, `INSERT INTO _sc_exec_test (val) VALUES ($1)`, "hello")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected, got %d", tag.RowsAffected())
	}
}

// TestIntegration_Select verifies Select scans multiple rows.
func TestIntegration_Select(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	_, _ = comp.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS _sc_select_test (id SERIAL PRIMARY KEY, val TEXT)`)
	t.Cleanup(func() {
		_, _ = comp.Exec(context.Background(), `DROP TABLE IF EXISTS _sc_select_test`)
	})
	_, _ = comp.Exec(ctx, `INSERT INTO _sc_select_test (val) VALUES ('a'), ('b'), ('c')`)

	type row struct {
		Val string `db:"val"`
	}
	var rows []row
	if err := comp.Select(ctx, &rows, `SELECT val FROM _sc_select_test ORDER BY val`); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Val != "a" || rows[1].Val != "b" || rows[2].Val != "c" {
		t.Fatalf("unexpected values: %+v", rows)
	}
}

// TestIntegration_Get verifies Get scans exactly one row.
func TestIntegration_Get(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	_, _ = comp.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS _sc_get_test (id SERIAL PRIMARY KEY, val TEXT)`)
	t.Cleanup(func() {
		_, _ = comp.Exec(context.Background(), `DROP TABLE IF EXISTS _sc_get_test`)
	})
	_, _ = comp.Exec(ctx, `INSERT INTO _sc_get_test (val) VALUES ('only')`)

	type row struct {
		Val string `db:"val"`
	}
	var r row
	if err := comp.Get(ctx, &r, `SELECT val FROM _sc_get_test LIMIT 1`); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if r.Val != "only" {
		t.Fatalf("expected %q, got %q", "only", r.Val)
	}
}

// TestIntegration_Get_NoRows verifies Get returns ErrNoRows on empty result.
func TestIntegration_Get_NoRows(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	var dst struct {
		Val string `db:"val"`
	}
	err := comp.Get(ctx, &dst, `SELECT 1 WHERE false`)
	if !errors.Is(err, postgresql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

// TestIntegration_Transaction_Commit verifies a committed transaction persists.
func TestIntegration_Transaction_Commit(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	_, _ = comp.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS _sc_tx_test (id SERIAL PRIMARY KEY, val TEXT)`)
	t.Cleanup(func() {
		_, _ = comp.Exec(context.Background(), `DROP TABLE IF EXISTS _sc_tx_test`)
	})

	tx, err := comp.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	_, execErr := tx.Exec(ctx, `INSERT INTO _sc_tx_test (val) VALUES ('committed')`)
	if err := comp.CommitTx(ctx, tx, execErr); err != nil {
		t.Fatalf("CommitTx: %v", err)
	}

	var dst struct {
		Val string `db:"val"`
	}
	if err := comp.Get(ctx, &dst, `SELECT val FROM _sc_tx_test LIMIT 1`); err != nil {
		t.Fatalf("Get after commit: %v", err)
	}
	if dst.Val != "committed" {
		t.Fatalf("expected %q, got %q", "committed", dst.Val)
	}
}

// TestIntegration_Transaction_Rollback verifies a rolled-back transaction
// leaves no trace.
func TestIntegration_Transaction_Rollback(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	_, _ = comp.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS _sc_txrb_test (id SERIAL PRIMARY KEY, val TEXT)`)
	t.Cleanup(func() {
		_, _ = comp.Exec(context.Background(), `DROP TABLE IF EXISTS _sc_txrb_test`)
	})

	tx, err := comp.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	_, _ = tx.Exec(ctx, `INSERT INTO _sc_txrb_test (val) VALUES ('should-vanish')`)
	// Force rollback by passing a non-nil error.
	inErr := errors.New("intentional failure")
	if err := comp.CommitTx(ctx, tx, inErr); !errors.Is(err, inErr) {
		t.Fatalf("expected inErr in chain, got %v", err)
	}

	var dst struct {
		Val string `db:"val"`
	}
	err = comp.Get(ctx, &dst, `SELECT val FROM _sc_txrb_test LIMIT 1`)
	if !errors.Is(err, postgresql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows after rollback, got %v", err)
	}
}
