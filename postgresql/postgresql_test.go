package postgresql_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/postgresql"
)

// ----------------------------------------------------------------------------
// Construction (no database needed)
// ----------------------------------------------------------------------------

func TestNew_DefaultName(t *testing.T) {
	comp := postgresql.New(postgresql.Config{})
	if comp.Name() != "postgres" {
		t.Fatalf("expected name %q, got %q", "postgres", comp.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	comp := postgresql.New(postgresql.Config{}, postgresql.WithName("postgres-replica"))
	if comp.Name() != "postgres-replica" {
		t.Fatalf("expected name %q, got %q", "postgres-replica", comp.Name())
	}
}

func TestNew_WithLogger(t *testing.T) {
	comp := postgresql.New(postgresql.Config{}, postgresql.WithLogger(&testLogger{t}))
	if comp == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// Lifecycle (no database needed)
// ----------------------------------------------------------------------------

func TestStop_BeforeStart(t *testing.T) {
	comp := postgresql.New(postgresql.Config{})
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { done <- comp.Stop(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked unexpectedly before Start")
	}
}

func TestStop_Idempotent(t *testing.T) {
	comp := postgresql.New(postgresql.Config{})
	ctx := context.Background()
	for range 3 {
		if err := comp.Stop(ctx); err != nil {
			t.Fatalf("repeated Stop returned error: %v", err)
		}
	}
}

// TestStart_UnreachableHost verifies Start fails and returns a non-nil error
// when the database cannot be reached.
func TestStart_UnreachableHost(t *testing.T) {
	comp := postgresql.New(postgresql.Config{
		Host:           "192.0.2.1", // TEST-NET — guaranteed unreachable
		ConnectTimeout: 300 * time.Millisecond,
	})
	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(ctx, func() {
			t.Error("ready() must not be called when connection fails")
		})
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil error from Start with unreachable host")
		}
		t.Logf("Start correctly returned: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline")
	}
}

func TestHealth_BeforeStart(t *testing.T) {
	comp := postgresql.New(postgresql.Config{})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("expected error from Health before Start")
	}
}

// ----------------------------------------------------------------------------
// CommitTx — tested via TxFinaliser stub, no real DB required
// ----------------------------------------------------------------------------

type fakeTx struct {
	committed   bool
	rolledBack  bool
	commitErr   error
	rollbackErr error
}

func (f *fakeTx) Commit(_ context.Context) error   { f.committed = true; return f.commitErr }
func (f *fakeTx) Rollback(_ context.Context) error { f.rolledBack = true; return f.rollbackErr }

func TestCommitTx_CommitsOnSuccess(t *testing.T) {
	comp := postgresql.New(postgresql.Config{}, postgresql.WithLogger(&testLogger{t}))
	fake := &fakeTx{}
	if err := comp.CommitTx(context.Background(), fake, nil); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !fake.committed {
		t.Fatal("expected Commit to be called")
	}
	if fake.rolledBack {
		t.Fatal("Rollback must not be called on success")
	}
}

func TestCommitTx_RollsBackOnError(t *testing.T) {
	comp := postgresql.New(postgresql.Config{}, postgresql.WithLogger(&testLogger{t}))
	inErr := errors.New("domain error")
	fake := &fakeTx{}
	err := comp.CommitTx(context.Background(), fake, inErr)
	if !errors.Is(err, inErr) {
		t.Fatalf("expected original error in chain, got %v", err)
	}
	if !fake.rolledBack {
		t.Fatal("expected Rollback to be called")
	}
	if fake.committed {
		t.Fatal("Commit must not be called when inErr != nil")
	}
}

func TestCommitTx_BothErrorsInChain(t *testing.T) {
	comp := postgresql.New(postgresql.Config{}, postgresql.WithLogger(&testLogger{t}))
	inErr := errors.New("original error")
	rbErr := errors.New("rollback also failed")
	fake := &fakeTx{rollbackErr: rbErr}
	err := comp.CommitTx(context.Background(), fake, inErr)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !errors.Is(err, rbErr) {
		t.Errorf("rollback error not in chain: %v", err)
	}
	if !errors.Is(err, inErr) {
		t.Errorf("original error not in chain: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type testLogger struct{ t *testing.T }

func (l *testLogger) Info(msg string, args ...any)  { l.t.Log(append([]any{"INFO ", msg}, args...)...) }
func (l *testLogger) Error(msg string, args ...any) { l.t.Log(append([]any{"ERROR", msg}, args...)...) }
