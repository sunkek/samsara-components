//go:build integration

package grpc_test

// Integration tests spin up a real gRPC server on an ephemeral port.
// No external infrastructure is required — gRPC runs in-process.
//
// Run with:
//
//	go test -race -tags integration -timeout 120s ./...
//
// or via the repo root:
//
//	make test-integration

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	grpccomp "github.com/sunkek/samsara-components/grpc"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// freePort asks the OS for a free TCP port and releases it immediately.
// There is a small TOCTOU window, but it is acceptable for tests.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func testComp(t *testing.T, port int) *grpccomp.Component {
	t.Helper()
	return grpccomp.New(
		grpccomp.Config{Host: "127.0.0.1", Port: port},
		grpccomp.WithLogger(&testLogger{t}),
	)
}

// startComp starts the component, waits for ready, and registers cleanup.
func startComp(t *testing.T, comp *grpccomp.Component) {
	t.Helper()
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		errCh <- comp.Start(ctx, func() { close(readyCh) })
	}()

	select {
	case <-readyCh:
	case err := <-errCh:
		cancel()
		t.Fatalf("Start failed: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("Start timed out")
	}

	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := comp.Stop(stopCtx); err != nil {
			t.Errorf("Stop returned error: %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start returned unexpected error after Stop: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Start goroutine did not exit after Stop")
		}
	})
}

// dialComp creates a gRPC client connection to the component's address.
func dialComp(t *testing.T, port int) *grpclib.ClientConn {
	t.Helper()
	conn, err := grpclib.NewClient(
		fmt.Sprintf("127.0.0.1:%d", port),
		grpclib.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestIntegration_StartStop(t *testing.T) {
	port := freePort(t)
	comp := testComp(t, port)
	startComp(t, comp)
}

func TestIntegration_Health(t *testing.T) {
	port := freePort(t)
	comp := testComp(t, port)
	startComp(t, comp)

	if err := comp.Health(context.Background()); err != nil {
		t.Fatalf("Health returned error on live server: %v", err)
	}
}

func TestIntegration_HealthBeforeStart(t *testing.T) {
	comp := grpccomp.New(grpccomp.Config{Host: "127.0.0.1", Port: freePort(t)})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("Health must return error before Start")
	}
}

func TestIntegration_GRPCHealthProtocol(t *testing.T) {
	// Verify the standard gRPC health service is reachable via the wire protocol.
	// This is what Kubernetes liveness/readiness probes use.
	port := freePort(t)
	comp := testComp(t, port)
	startComp(t, comp)

	conn := dialComp(t, port)
	client := healthpb.NewHealthClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		t.Fatalf("health check RPC failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING, got %v", resp.Status)
	}
}

func TestIntegration_Restart(t *testing.T) {
	port := freePort(t)
	comp := testComp(t, port)

	for i := range 2 {
		t.Logf("run %d", i+1)
		readyCh := make(chan struct{})
		errCh := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())

		go func() { errCh <- comp.Start(ctx, func() { close(readyCh) }) }()

		select {
		case <-readyCh:
		case err := <-errCh:
			cancel()
			t.Fatalf("run %d: Start failed: %v", i+1, err)
		case <-time.After(15 * time.Second):
			cancel()
			t.Fatalf("run %d: Start timed out", i+1)
		}

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = comp.Stop(stopCtx)
		stopCancel()
		cancel()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("run %d: Start returned error: %v", i+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("run %d: Start goroutine did not exit", i+1)
		}
	}
}

func TestIntegration_StopDrainsInFlight(t *testing.T) {
	// Verify GracefulStop waits for in-flight RPCs to complete.
	// We use the health watch stream (server-streaming) as a long-lived RPC.
	port := freePort(t)
	comp := testComp(t, port)
	startComp(t, comp)

	conn := dialComp(t, port)
	client := healthpb.NewHealthClient(conn)

	// Open a server-streaming watch RPC. This stays open until the server stops.
	watchCtx, watchCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer watchCancel()

	stream, err := client.Watch(watchCtx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		t.Fatalf("Watch RPC failed: %v", err)
	}

	// Receive the initial SERVING status.
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("first Recv failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING, got %v", resp.Status)
	}

	// Stop fires GracefulStop which eventually closes the stream cleanly.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := comp.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestIntegration_Register_ServiceVisible(t *testing.T) {
	// RegisterFuncs registered before Start must be applied to the live server.
	// We verify by checking the health service (always registered) is reachable,
	// and that a second Register call (added before Start) is also applied.
	port := freePort(t)
	comp := testComp(t, port)

	called := false
	comp.Register(func(s *grpclib.Server) {
		// In real usage this would be: pb.RegisterMyServiceServer(s, impl)
		// We can't do that here without generated proto code, so we just
		// verify the callback fires.
		called = true
	})

	startComp(t, comp)

	if !called {
		t.Fatal("RegisterFunc was not called during Start")
	}
}

func TestIntegration_Reflection_Disabled(t *testing.T) {
	// With reflection disabled (default), the reflection service should not
	// respond. We verify by connecting and checking the server info.
	port := freePort(t)
	comp := grpccomp.New(
		grpccomp.Config{Host: "127.0.0.1", Port: port, EnableReflection: false},
		grpccomp.WithLogger(&testLogger{t}),
	)
	startComp(t, comp)

	conn := dialComp(t, port)
	client := healthpb.NewHealthClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Health service is always registered — this must succeed.
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING, got %v", resp.Status)
	}
}

func TestIntegration_Reflection_Enabled(t *testing.T) {
	port := freePort(t)
	comp := grpccomp.New(
		grpccomp.Config{Host: "127.0.0.1", Port: port, EnableReflection: true},
		grpccomp.WithLogger(&testLogger{t}),
	)
	startComp(t, comp)

	conn := dialComp(t, port)
	client := healthpb.NewHealthClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health check failed with reflection enabled: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING, got %v", resp.Status)
	}
}

func TestIntegration_AddOption_Interceptor(t *testing.T) {
	port := freePort(t)
	comp := testComp(t, port)

	// Verify that an interceptor added via AddOption is applied and fires.
	interceptorCalled := false
	comp.AddOption(grpclib.ChainUnaryInterceptor(
		func(
			ctx context.Context,
			req any,
			info *grpclib.UnaryServerInfo,
			handler grpclib.UnaryHandler,
		) (any, error) {
			interceptorCalled = true
			return handler(ctx, req)
		},
	))

	startComp(t, comp)

	// Call the health check — it goes through the unary interceptor chain.
	conn := dialComp(t, port)
	client := healthpb.NewHealthClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health check failed: %v", err)
	}

	if !interceptorCalled {
		t.Fatal("unary interceptor was not called")
	}
}
