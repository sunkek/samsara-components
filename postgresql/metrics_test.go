package postgresql

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// observation is one call recorded by the test sink.
type observation struct {
	op  string
	d   time.Duration
	err error
}

// recorder is a Config.OnOperation sink that captures every call.
type recorder struct {
	mu   sync.Mutex
	obs  []observation
	hook func()
}

func (r *recorder) record(op string, d time.Duration, err error) {
	if r.hook != nil {
		r.hook()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.obs = append(r.obs, observation{op, d, err})
}

func (r *recorder) all() []observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observation(nil), r.obs...)
}

// nopTx is a TxFinaliser that succeeds without a database.
type nopTx struct{ commits, rollbacks int }

func (t *nopTx) Commit(context.Context) error   { t.commits++; return nil }
func (t *nopTx) Rollback(context.Context) error { t.rollbacks++; return nil }

// Unlike redis and sqlite, this component has no not-ready sentinel: the pool
// operations panic on an unstarted component rather than returning an error,
// and instrumentation deliberately did not change that. CommitTx acts on the
// transaction rather than the pool, so it is the one operation whose reporting
// can be proven without a live database; the rest are covered by the
// integration tests.

func TestOnOperation_ReportsCommitTx(t *testing.T) {
	r := &recorder{}
	c := New(Config{OnOperation: r.record})

	tx := &nopTx{}
	if err := c.CommitTx(context.Background(), tx, nil); err != nil {
		t.Fatalf("CommitTx error = %v, want nil", err)
	}
	if tx.commits != 1 {
		t.Errorf("commits = %d, want 1", tx.commits)
	}

	got := r.all()
	if len(got) != 1 {
		t.Fatalf("got %d observations, want 1: %+v", len(got), got)
	}
	if got[0].op != "postgres.commit_tx" {
		t.Errorf("op = %q, want %q", got[0].op, "postgres.commit_tx")
	}
	if got[0].err != nil {
		t.Errorf("reported error = %v, want nil", got[0].err)
	}
}

func TestOnOperation_CommitTxRollbackReportsCallerError(t *testing.T) {
	r := &recorder{}
	c := New(Config{OnOperation: r.record})

	cause := errors.New("business rule violated")
	tx := &nopTx{}
	if err := c.CommitTx(context.Background(), tx, cause); !errors.Is(err, cause) {
		t.Fatalf("CommitTx error = %v, want the caller's cause", err)
	}
	if tx.rollbacks != 1 {
		t.Errorf("rollbacks = %d, want 1", tx.rollbacks)
	}

	got := r.all()
	if len(got) != 1 {
		t.Fatalf("got %d observations, want 1", len(got))
	}
	if !errors.Is(got[0].err, cause) {
		t.Errorf("reported error = %v, want the caller's cause", got[0].err)
	}
}

func TestOnOperation_NilSinkIsNoOp(t *testing.T) {
	c := New(Config{})
	if err := c.CommitTx(context.Background(), &nopTx{}, nil); err != nil {
		t.Fatalf("CommitTx error = %v, want nil", err)
	}
}

func TestOnOperation_PanickingSinkDoesNotReachCaller(t *testing.T) {
	r := &recorder{hook: func() { panic("sink exploded") }}
	c := New(Config{OnOperation: r.record})

	// The operation's own result must survive the sink's panic unchanged.
	if err := c.CommitTx(context.Background(), &nopTx{}, nil); err != nil {
		t.Fatalf("CommitTx error = %v, want nil despite panicking sink", err)
	}
}

func TestConfig_ZeroValueLeavesMetricsDisabled(t *testing.T) {
	if (Config{}).OnOperation != nil {
		t.Error("zero-value Config should not report metrics")
	}
}
