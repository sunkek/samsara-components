package rabbitmq

import (
	"context"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRetryPolicy_Defaults(t *testing.T) {
	var p RetryPolicy
	if got := p.maxRetries(); got != 3 {
		t.Fatalf("maxRetries default = %d, want 3", got)
	}
	if got := p.backoff(1); got != time.Second {
		t.Fatalf("backoff(1) = %v, want 1s", got)
	}
	if got := p.backoff(2); got != 2*time.Second {
		t.Fatalf("backoff(2) = %v, want 2s", got)
	}
	if got := p.retryQueue("orders"); got != "orders.retry" {
		t.Fatalf("retryQueue = %q, want orders.retry", got)
	}
	if got := p.dlq("orders"); got != "orders.dlq" {
		t.Fatalf("dlq = %q, want orders.dlq", got)
	}
}

func TestRetryPolicy_Overrides(t *testing.T) {
	p := RetryPolicy{
		MaxRetries:        5,
		Backoff:           100 * time.Millisecond,
		BackoffMultiplier: 3,
		MaxBackoff:        500 * time.Millisecond,
		RetryQueue:        "custom.retry",
		DLQ:               "custom.dlq",
	}
	if got := p.maxRetries(); got != 5 {
		t.Fatalf("maxRetries = %d, want 5", got)
	}
	if got := p.backoff(1); got != 100*time.Millisecond {
		t.Fatalf("backoff(1) = %v, want 100ms", got)
	}
	if got := p.backoff(2); got != 300*time.Millisecond {
		t.Fatalf("backoff(2) = %v, want 300ms", got)
	}
	if got := p.backoff(10); got != 500*time.Millisecond {
		t.Fatalf("backoff(10) = %v, want capped 500ms", got)
	}
	if got := p.retryQueue("q"); got != "custom.retry" {
		t.Fatalf("retryQueue = %q", got)
	}
	if got := p.dlq("q"); got != "custom.dlq" {
		t.Fatalf("dlq = %q", got)
	}
}

func TestRetryPolicy_NegativeMaxRetries(t *testing.T) {
	p := RetryPolicy{MaxRetries: -1}
	if got := p.maxRetries(); got != 0 {
		t.Fatalf("maxRetries = %d, want 0", got)
	}
}

func TestRetryCount(t *testing.T) {
	if got := retryCount(nil); got != 0 {
		t.Fatalf("nil headers = %d, want 0", got)
	}
	for _, v := range []any{int32(2), int64(2), int(2)} {
		if got := retryCount(amqp.Table{RetryHeader: v}); got != 2 {
			t.Fatalf("retryCount(%T) = %d, want 2", v, got)
		}
	}
	if got := retryCount(amqp.Table{RetryHeader: "bogus"}); got != 0 {
		t.Fatalf("bogus header = %d, want 0", got)
	}
}

func TestHealth_DeadConsumer(t *testing.T) {
	c := New(Config{})
	c.markConsumerDead("orders", "delivery channel closed by broker")
	err := c.Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), `queue "orders"`) {
		t.Fatalf("Health = %v, want dead-consumer error", err)
	}
}
