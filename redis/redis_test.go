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

// TestErrNotReady verifies that every Client operation returns ErrNotReady —
// not a nil-pointer panic — when the component has no live connection (before
// Start, after Stop, or during a restart while Redis is down).
func TestErrNotReady_NoPanic(t *testing.T) {
	c := redis.New(redis.Config{}) // never Started: client is nil
	ctx := context.Background()

	t.Run("Set", func(t *testing.T) {
		if err := c.Set(ctx, "k", "v", 0); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("Set: want ErrNotReady, got %v", err)
		}
	})
	t.Run("SetNX", func(t *testing.T) {
		if _, err := c.SetNX(ctx, "k", "v", 0); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("SetNX: want ErrNotReady, got %v", err)
		}
	})
	t.Run("Get", func(t *testing.T) {
		if _, err := c.Get(ctx, "k"); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("Get: want ErrNotReady, got %v", err)
		}
	})
	t.Run("Del", func(t *testing.T) {
		if _, err := c.Del(ctx, "k"); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("Del: want ErrNotReady, got %v", err)
		}
	})
	t.Run("Exists", func(t *testing.T) {
		if _, err := c.Exists(ctx, "k"); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("Exists: want ErrNotReady, got %v", err)
		}
	})
	t.Run("Expire", func(t *testing.T) {
		if _, err := c.Expire(ctx, "k", time.Second); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("Expire: want ErrNotReady, got %v", err)
		}
	})
	t.Run("TTL", func(t *testing.T) {
		if _, err := c.TTL(ctx, "k"); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("TTL: want ErrNotReady, got %v", err)
		}
	})
	t.Run("Scan", func(t *testing.T) {
		if _, err := c.Scan(ctx, "*"); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("Scan: want ErrNotReady, got %v", err)
		}
	})
	t.Run("Health", func(t *testing.T) {
		if err := c.Health(ctx); !errors.Is(err, redis.ErrNotReady) {
			t.Fatalf("Health: want ErrNotReady, got %v", err)
		}
	})
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type testLogger struct{ t *testing.T }

func (l *testLogger) Debug(msg string, args ...any) { l.t.Log(append([]any{"DEBUG", msg}, args...)...) }
func (l *testLogger) Info(msg string, args ...any)  { l.t.Log(append([]any{"INFO ", msg}, args...)...) }
func (l *testLogger) Warn(msg string, args ...any)  { l.t.Log(append([]any{"WARN ", msg}, args...)...) }
func (l *testLogger) Error(msg string, args ...any) { l.t.Log(append([]any{"ERROR", msg}, args...)...) }

// ----------------------------------------------------------------------------
// Config
// ----------------------------------------------------------------------------

// A zero-value Config must produce a usable component: every default is
// supplied by an unexported accessor at the point of use, so New never needs a
// populated Config.
func TestConfig_ZeroValueNoPanic(t *testing.T) {
	c := redis.New(redis.Config{})
	if c == nil {
		t.Fatal("expected non-nil component")
	}
	if c.Name() == "" {
		t.Error("expected a default name")
	}
}
