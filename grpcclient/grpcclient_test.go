package grpcclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/grpcclient"
	grpclib "google.golang.org/grpc"
)

// ----------------------------------------------------------------------------
// Construction
// ----------------------------------------------------------------------------

func TestNew_DefaultName(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"})
	if comp.Name() != "grpc-client" {
		t.Fatalf("expected name %q, got %q", "grpc-client", comp.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"},
		grpcclient.WithName("user-service-client"))
	if comp.Name() != "user-service-client" {
		t.Fatalf("expected %q, got %q", "user-service-client", comp.Name())
	}
}

func TestNew_WithLogger(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"},
		grpcclient.WithLogger(&testLogger{t}))
	if comp == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// Conn before Start
// ----------------------------------------------------------------------------

func TestConn_BeforeStart_ReturnsNil(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"})
	if comp.Conn() != nil {
		t.Fatal("expected nil Conn before Start")
	}
}

// ----------------------------------------------------------------------------
// Lifecycle (no real server needed)
// ----------------------------------------------------------------------------

func TestStop_BeforeStart(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"})
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
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"})
	ctx := context.Background()
	for range 3 {
		if err := comp.Stop(ctx); err != nil {
			t.Fatalf("repeated Stop returned error: %v", err)
		}
	}
}

func TestStart_UnreachableTarget(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{
		Target:         "192.0.2.1:9090", // TEST-NET — guaranteed unreachable
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
			t.Fatal("expected non-nil error from Start with unreachable target")
		}
		t.Logf("Start correctly returned: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline")
	}
}

func TestStart_ContextCancelledBeforeConnect(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{
		Target:         "192.0.2.1:9090",
		ConnectTimeout: 10 * time.Second, // long timeout
	})
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
		// nil is correct: clean shutdown means Start returns nil
		t.Logf("Start returned: %v (nil = clean shutdown)", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline after context cancel")
	}
}

func TestHealth_BeforeStart(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("expected error from Health before Start")
	}
}

// ----------------------------------------------------------------------------
// AddOption (no connection — only verifies no panic)
// ----------------------------------------------------------------------------

func TestAddOption_BeforeStart(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "localhost:9090"})
	comp.AddOption(grpclib.WithChainUnaryInterceptor())  // empty chain — valid
	comp.AddOption(grpclib.WithChainStreamInterceptor()) // ditto
}

// ----------------------------------------------------------------------------
// Config defaults
// ----------------------------------------------------------------------------

func TestConfig_EmptyTarget_FailsOnStart(t *testing.T) {
	// An empty target should cause Start to fail immediately.
	comp := grpcclient.New(grpcclient.Config{
		Target:         "",
		ConnectTimeout: 300 * time.Millisecond,
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(context.Background(), func() {
			t.Error("ready() must not be called with empty target")
		})
	}()
	select {
	case err := <-errCh:
		// Either an immediate error from NewClient, or a connect timeout.
		// Both are non-nil — empty target must not succeed.
		if err == nil {
			t.Fatal("expected non-nil error from Start with empty target")
		}
		t.Logf("Start correctly returned: %v", err)
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
func (l *testLogger) Warn(msg string, args ...any) {
	l.t.Log(append([]any{"WARN ", msg}, args...)...)
}
func (l *testLogger) Error(msg string, args ...any) {
	l.t.Log(append([]any{"ERROR", msg}, args...)...)
}
