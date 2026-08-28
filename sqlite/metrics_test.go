package sqlite

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

func (t *nopTx) Commit() error   { t.commits++; return nil }
func (t *nopTx) Rollback() error { t.rollbacks++; return nil }

func TestOnOperation_ReportsEveryDBOperation(t *testing.T) {
	ctx := context.Background()
	r := &recorder{}
	c := New(Config{OnOperation: r.record})

	// Not started, so the pool operations take the not-started path. CommitTx
	// acts on the transaction, not the pool, so it runs for real.
	var dst []int
	_ = c.Select(ctx, &dst, "SELECT 1")
	_ = c.Get(ctx, &dst, "SELECT 1")
	_, _ = c.Exec(ctx, "SELECT 1")
	_, _ = c.BeginTx(ctx, nil)
	_ = c.CommitTx(&nopTx{}, nil)

	want := []string{
		"sqlite.select", "sqlite.get", "sqlite.exec",
		"sqlite.begin_tx", "sqlite.commit_tx",
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

func TestOnOperation_NotStartedReportsZeroDuration(t *testing.T) {
	r := &recorder{}
	c := New(Config{OnOperation: r.record})

	var dst []int
	if err := c.Select(context.Background(), &dst, "SELECT 1"); !errors.Is(err, errNotStarted) {
		t.Fatalf("Select error = %v, want errNotStarted", err)
	}

	got := r.all()
	if len(got) != 1 {
		t.Fatalf("got %d observations, want 1", len(got))
	}
	// The operation was never attempted, so there is no driver call to time.
	if got[0].d != 0 {
		t.Errorf("reported duration = %v, want 0 for an unattempted operation", got[0].d)
	}
}

func TestOnOperation_CommitTxRollbackReportsCallerError(t *testing.T) {
	r := &recorder{}
	c := New(Config{OnOperation: r.record})

	cause := errors.New("business rule violated")
	tx := &nopTx{}
	if err := c.CommitTx(tx, cause); !errors.Is(err, cause) {
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
	var dst []int
	if err := c.Select(context.Background(), &dst, "SELECT 1"); !errors.Is(err, errNotStarted) {
		t.Fatalf("Select error = %v, want errNotStarted", err)
	}
}

func TestOnOperation_PanickingSinkDoesNotReachCaller(t *testing.T) {
	r := &recorder{hook: func() { panic("sink exploded") }}
	c := New(Config{OnOperation: r.record})

	// The operation's own error must survive the sink's panic unchanged.
	_, err := c.Exec(context.Background(), "SELECT 1")
	if !errors.Is(err, errNotStarted) {
		t.Fatalf("Exec error = %v, want errNotStarted despite panicking sink", err)
	}
}

func TestConfig_ZeroValueLeavesMetricsDisabled(t *testing.T) {
	if (Config{}).OnOperation != nil {
		t.Error("zero-value Config should not report metrics")
	}
}
