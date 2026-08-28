package rabbitmq_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/sunkek/samsara-components/rabbitmq"
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

func TestOnOperation_ReportsEveryPublisherCall(t *testing.T) {
	ctx := context.Background()
	r := &recorder{}
	c := rabbitmq.New(rabbitmq.Config{OnOperation: r.record})

	// Never started, so every publish fails on the missing channel — enough to
	// prove each one reports under its own name, without a live broker.
	_ = c.Publish(ctx, "ex", "rk", rabbitmq.ContentTypeJSON, []byte("{}"))
	_ = c.PublishWithType(ctx, "ex", "rk", rabbitmq.ContentTypeJSON, "order.created", []byte("{}"))
	_ = c.PublishWithHeaders(ctx, "ex", "rk", rabbitmq.ContentTypeJSON, amqp.Table{"n": 1}, []byte("{}"))

	want := []string{
		"rabbitmq.publish", "rabbitmq.publish_with_type", "rabbitmq.publish_with_headers",
	}
	got := r.all()
	if len(got) != len(want) {
		t.Fatalf("got %d observations, want %d: %+v", len(got), len(want), got)
	}
	for i, op := range want {
		if got[i].op != op {
			t.Errorf("observation %d: op = %q, want %q", i, got[i].op, op)
		}
		if !errors.Is(got[i].err, rabbitmq.ErrNotReady) {
			t.Errorf("observation %d (%s): reported error = %v, want ErrNotReady", i, op, got[i].err)
		}
	}
}

func TestOnOperation_NilSinkIsNoOp(t *testing.T) {
	c := rabbitmq.New(rabbitmq.Config{})
	if err := c.Publish(context.Background(), "ex", "rk", rabbitmq.ContentTypeJSON, []byte("{}")); !errors.Is(err, rabbitmq.ErrNotReady) {
		t.Fatalf("Publish error = %v, want ErrNotReady", err)
	}
}

func TestOnOperation_PanickingSinkDoesNotReachCaller(t *testing.T) {
	r := &recorder{hook: func() { panic("sink exploded") }}
	c := rabbitmq.New(rabbitmq.Config{OnOperation: r.record})

	// The operation's own error must survive the sink's panic unchanged.
	if err := c.Publish(context.Background(), "ex", "rk", rabbitmq.ContentTypeJSON, []byte("{}")); !errors.Is(err, rabbitmq.ErrNotReady) {
		t.Fatalf("Publish error = %v, want ErrNotReady despite panicking sink", err)
	}
}

func TestConfig_ZeroValueLeavesMetricsDisabled(t *testing.T) {
	if (rabbitmq.Config{}).OnOperation != nil {
		t.Error("zero-value Config should not report metrics")
	}
}
