package postgresql

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func TestOnOperation_ReportsEveryDBOperation(t *testing.T) {
	ctx := context.Background()
	r := &recorder{}
	c := New(Config{OnOperation: r.record})

	// Not started, so the pool operations take the not-ready path. CommitTx
	// acts on the transaction, not the pool, so it runs for real.
	var dst []int
	_ = c.Select(ctx, &dst, "SELECT 1")
	_ = c.Get(ctx, &dst, "SELECT 1")
	_, _ = c.Exec(ctx, "SELECT 1")
	_, _ = c.BeginTx(ctx, pgx.TxOptions{})
	_ = c.CommitTx(ctx, &nopTx{}, nil)

	want := []string{
		"postgres.select", "postgres.get", "postgres.exec",
		"postgres.begin_tx", "postgres.commit_tx",
	}
	got := r.all()
	if len(got) != len(want) {
		t.Fatalf("got %d observations, want %d: %+v", len(got), len(want), got)
	}
	for i, op := range want {
		if got[i].op != op {
			t.Errorf("observation %d: op = %q, want %q", i, got[i].op, op)
		}
	}
}

// Every pool operation returns ErrNotReady rather than panicking on a nil
// pool, and reports a zero duration because no driver call was made.
func TestOnOperation_NotReadyReportsSentinelAndZeroDuration(t *testing.T) {
	ctx := context.Background()
	r := &recorder{}
	c := New(Config{OnOperation: r.record})

	var dst []int
	if err := c.Select(ctx, &dst, "SELECT 1"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Select before Start = %v, want ErrNotReady", err)
	}
	if err := c.Get(ctx, &dst, "SELECT 1"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Get before Start = %v, want ErrNotReady", err)
	}
	if _, err := c.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Exec before Start = %v, want ErrNotReady", err)
	}
	if _, err := c.BeginTx(ctx, pgx.TxOptions{}); !errors.Is(err, ErrNotReady) {
		t.Errorf("BeginTx before Start = %v, want ErrNotReady", err)
	}

	for _, o := range r.all() {
		if !errors.Is(o.err, ErrNotReady) {
			t.Errorf("%s: reported error = %v, want ErrNotReady", o.op, o.err)
		}
		if o.d != 0 {
			t.Errorf("%s: reported duration = %v, want 0 for an unattempted operation", o.op, o.d)
		}
	}
}

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
