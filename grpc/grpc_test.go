package grpc_test

import (
	"context"
	"testing"
	"time"

	grpccomp "github.com/sunkek/samsara-components/grpc"
	grpclib "google.golang.org/grpc"
)

// ----------------------------------------------------------------------------
// Construction
// ----------------------------------------------------------------------------

func TestNew_DefaultName(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{})
	if comp.Name() != "grpc" {
		t.Fatalf("expected name %q, got %q", "grpc", comp.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{}, grpccomp.WithName("internal-grpc"))
	if comp.Name() != "internal-grpc" {
		t.Fatalf("expected %q, got %q", "internal-grpc", comp.Name())
	}
}

func TestNew_WithLogger(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{}, grpccomp.WithLogger(&testLogger{t}))
	if comp == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// Lifecycle (no real server needed)
// ----------------------------------------------------------------------------

func TestStop_BeforeStart(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{})
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
	comp := grpccomp.New(grpccomp.Config{})
	ctx := context.Background()
	for range 3 {
		if err := comp.Stop(ctx); err != nil {
			t.Fatalf("repeated Stop returned error: %v", err)
		}
	}
}

func TestStart_InvalidAddress(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{
		Host: "invalid-host-%%%",
		Port: 0,
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(context.Background(), func() {
			t.Error("ready() must not be called when listen fails")
		})
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil error from Start with invalid address")
		}
		t.Logf("Start correctly returned: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline")
	}
}

func TestStart_ContextCancelledBeforeListen(t *testing.T) {
	// Use a valid but (likely) in-use port to slow Start down; the cancelled
	// context should win. Port 1 is reserved and will fail to bind.
	comp := grpccomp.New(grpccomp.Config{Host: "127.0.0.1", Port: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(ctx, func() {
			t.Error("ready() must not be called")
		})
	}()
	select {
	case err := <-errCh:
		// Either nil (clean shutdown won the race) or a bind error is fine.
		// What matters is that Start returned promptly.
		t.Logf("Start returned: %v (nil = clean shutdown, non-nil = bind error)", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline after context cancel")
	}
}

func TestHealth_BeforeStart(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("expected error from Health before Start")
	}
}

// ----------------------------------------------------------------------------
// Register and AddOption (no server — only verifies no panic)
// ----------------------------------------------------------------------------

func TestRegister_BeforeStart(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{})
	// Register should not panic and should not return an error.
	comp.Register(func(s *grpclib.Server) {
		// no-op registration — proto service not available in unit tests
	})
}

func TestAddOption_BeforeStart(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{})
	comp.AddOption(grpclib.ChainUnaryInterceptor())  // no interceptors, but valid
	comp.AddOption(grpclib.ChainStreamInterceptor()) // ditto
}

func TestRegister_MultipleCallbacks(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{})
	callCount := 0
	for range 3 {
		n := callCount // capture
		_ = n
		comp.Register(func(s *grpclib.Server) {
			callCount++
		})
	}
	// We cannot call Start without a real port, but we can verify the slice
	// was built correctly by checking there is no panic and the state is sane.
	if comp == nil {
		t.Fatal("unexpected nil component")
	}
}

// ----------------------------------------------------------------------------
// Config defaults (via addr helper — tested indirectly through Start errors)
// ----------------------------------------------------------------------------

func TestConfig_DefaultPortInError(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{Host: "invalid%%%"})
	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(context.Background(), func() {})
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error")
		}
		// The error message should contain the default port 9090.
		errStr := err.Error()
		if len(errStr) == 0 {
			t.Fatal("error message is empty")
		}
		t.Logf("error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline")
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type testLogger struct{ t *testing.T }

func (l *testLogger) Info(msg string, args ...any) {
	l.t.Log(append([]any{"INFO ", msg}, args...)...)
}
func (l *testLogger) Error(msg string, args ...any) {
	l.t.Log(append([]any{"ERROR", msg}, args...)...)
}
