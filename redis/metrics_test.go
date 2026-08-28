package redis_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/redis"
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

// newRecorded builds a component wired to a fresh recorder. The component is
// never started, so every operation takes the not-ready path — enough to prove
// the helper reports, without a live server.
func newRecorded(t *testing.T) (*redis.Component, *recorder) {
	t.Helper()
	r := &recorder{}
	return redis.New(redis.Config{OnOperation: r.record}), r
}

func TestOnOperation_ReportsEveryKVOperation(t *testing.T) {
	ctx := context.Background()
	c, r := newRecorded(t)

	// Every method on the KV interface, in interface order.
	_ = c.Set(ctx, "k", "v", 0)
	_, _ = c.SetNX(ctx, "k", "v", 0)
	_, _ = c.Get(ctx, "k")
	_, _ = c.Del(ctx, "k")
	_, _ = c.Exists(ctx, "k")
	_, _ = c.Incr(ctx, "k")
	_, _ = c.Expire(ctx, "k", time.Second)
	_, _ = c.TTL(ctx, "k")
	_, _ = c.Scan(ctx, "*")

	want := []string{
		"redis.set", "redis.setnx", "redis.get", "redis.del", "redis.exists",
		"redis.incr", "redis.expire", "redis.ttl", "redis.scan",
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

func TestOnOperation_NotReadyReportsSentinelAndZeroDuration(t *testing.T) {
	c, r := newRecorded(t)

	if err := c.Set(context.Background(), "k", "v", 0); !errors.Is(err, redis.ErrNotReady) {
		t.Fatalf("Set error = %v, want ErrNotReady", err)
	}

	got := r.all()
	if len(got) != 1 {
		t.Fatalf("got %d observations, want 1", len(got))
	}
	if !errors.Is(got[0].err, redis.ErrNotReady) {
		t.Errorf("reported error = %v, want ErrNotReady", got[0].err)
	}
	// The operation was never attempted, so there is no driver call to time.
	if got[0].d != 0 {
		t.Errorf("reported duration = %v, want 0 for an unattempted operation", got[0].d)
	}
}

func TestOnOperation_NilSinkIsNoOp(t *testing.T) {
	c := redis.New(redis.Config{})
	if err := c.Set(context.Background(), "k", "v", 0); !errors.Is(err, redis.ErrNotReady) {
		t.Fatalf("Set error = %v, want ErrNotReady", err)
	}
}

func TestOnOperation_PanickingSinkDoesNotReachCaller(t *testing.T) {
	r := &recorder{hook: func() { panic("sink exploded") }}
	c := redis.New(redis.Config{OnOperation: r.record})

	// The operation's own error must survive the sink's panic unchanged.
	_, err := c.Get(context.Background(), "k")
	if !errors.Is(err, redis.ErrNotReady) {
		t.Fatalf("Get error = %v, want ErrNotReady despite panicking sink", err)
	}
}

func TestConfig_ZeroValueLeavesMetricsDisabled(t *testing.T) {
	if (redis.Config{}).OnOperation != nil {
		t.Error("zero-value Config should not report metrics")
	}
}
