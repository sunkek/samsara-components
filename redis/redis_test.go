package redis_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/redis"
)

// ----------------------------------------------------------------------------
// Construction
// ----------------------------------------------------------------------------

func TestNew_DefaultName(t *testing.T) {
	comp := redis.New(redis.Config{})
	if comp.Name() != "redis" {
		t.Fatalf("expected name %q, got %q", "redis", comp.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	comp := redis.New(redis.Config{}, redis.WithName("session-store"))
	if comp.Name() != "session-store" {
		t.Fatalf("expected %q, got %q", "session-store", comp.Name())
	}
}

func TestNew_WithLogger(t *testing.T) {
	comp := redis.New(redis.Config{}, redis.WithLogger(&testLogger{t}))
	if comp == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// Lifecycle (no server needed)
// ----------------------------------------------------------------------------

func TestStop_BeforeStart(t *testing.T) {
	comp := redis.New(redis.Config{})
	done := make(chan error, 1)
	go func() { done <- comp.Stop(context.Background()) }()
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
	comp := redis.New(redis.Config{})
	ctx := context.Background()
	for range 3 {
		if err := comp.Stop(ctx); err != nil {
			t.Fatalf("repeated Stop returned error: %v", err)
		}
	}
}

func TestStart_UnreachableHost(t *testing.T) {
	comp := redis.New(redis.Config{
		Host:           "192.0.2.1", // TEST-NET — guaranteed unreachable
		ConnectTimeout: 300 * time.Millisecond,
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(context.Background(), func() {
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
	comp := redis.New(redis.Config{})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("expected error from Health before Start")
	}
}

// ----------------------------------------------------------------------------
// Interface compliance
// ----------------------------------------------------------------------------

func TestComponent_ImplementsClient(t *testing.T) {
	var _ redis.Client = (*redis.Component)(nil)
}

// ----------------------------------------------------------------------------
// Sentinel errors
// ----------------------------------------------------------------------------

// TestErrNil verifies that ErrNil is a stable, distinct sentinel that callers
// can reliably detect with errors.Is.
func TestErrNil_Sentinel(t *testing.T) {
	// ErrNil must be detectable via errors.Is — this is the primary contract.
	if !errors.Is(redis.ErrNil, redis.ErrNil) {
		t.Fatal("errors.Is(ErrNil, ErrNil) must be true")
	}
	// ErrNil must not match unrelated errors.
	if errors.Is(redis.ErrNil, context.Canceled) {
		t.Fatal("ErrNil must not match context.Canceled")
	}
	if errors.Is(redis.ErrNil, context.DeadlineExceeded) {
		t.Fatal("ErrNil must not match context.DeadlineExceeded")
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type testLogger struct{ t *testing.T }

func (l *testLogger) Info(msg string, args ...any)  { l.t.Log(append([]any{"INFO ", msg}, args...)...) }
func (l *testLogger) Error(msg string, args ...any) { l.t.Log(append([]any{"ERROR", msg}, args...)...) }
