//go:build integration

package grpcclient_test

// Integration tests spin up a real gRPC server in-process and connect to it.
// No external infrastructure is required.
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

	"github.com/sunkek/samsara-components/grpcclient"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// ----------------------------------------------------------------------------
// In-process server helpers
// ----------------------------------------------------------------------------

// servingHealthImpl is a minimal health server that always returns SERVING.
type servingHealthImpl struct {
	healthpb.UnimplementedHealthServer
}

func (s *servingHealthImpl) Check(_ context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

// startTestServer starts a bare gRPC server (no services) on an ephemeral
// port and returns the address. Stopped when the test completes.
func startTestServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpclib.NewServer()
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.GracefulStop() })
	return ln.Addr().String()
}

// startHealthServer starts a gRPC server with the health service registered.
func startHealthServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpclib.NewServer()
	healthpb.RegisterHealthServer(srv, &servingHealthImpl{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.GracefulStop() })
	return ln.Addr().String()
}

// ----------------------------------------------------------------------------
// Component helpers
// ----------------------------------------------------------------------------

func testComp(t *testing.T, target string) *grpcclient.Component {
	t.Helper()
	return grpcclient.New(
		grpcclient.Config{Target: target, ConnectTimeout: 10 * time.Second},
		grpcclient.WithLogger(&testLogger{t}),
	)
}

// startComp starts the component, waits for ready, and registers cleanup.
func startComp(t *testing.T, comp *grpcclient.Component) {
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

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestIntegration_StartStop(t *testing.T) {
	addr := startTestServer(t)
	comp := testComp(t, addr)
	startComp(t, comp)
}

func TestIntegration_ConnIsNonNilAfterStart(t *testing.T) {
	addr := startTestServer(t)
	comp := testComp(t, addr)
	startComp(t, comp)

	if comp.Conn() == nil {
		t.Fatal("expected non-nil Conn after Start")
	}
}

func TestIntegration_ConnIsNilAfterStop(t *testing.T) {
	addr := startTestServer(t)
	comp := testComp(t, addr)

	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { errCh <- comp.Start(ctx, func() { close(readyCh) }) }()

	select {
	case <-readyCh:
	case err := <-errCh:
		t.Fatalf("Start failed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Start timed out")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := comp.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	if comp.Conn() != nil {
		t.Fatal("expected nil Conn after Stop")
	}
}

func TestIntegration_Health_Ready(t *testing.T) {
	addr := startTestServer(t)
	comp := testComp(t, addr)
	startComp(t, comp)

	if err := comp.Health(context.Background()); err != nil {
		t.Fatalf("Health returned error on live connection: %v", err)
	}
}

func TestIntegration_Health_BeforeStart(t *testing.T) {
	comp := grpcclient.New(grpcclient.Config{Target: "127.0.0.1:9090"})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("Health must return error before Start")
	}
}

func TestIntegration_Restart(t *testing.T) {
	addr := startTestServer(t)
	comp := testComp(t, addr)

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

func TestIntegration_ConnState_ReadyAfterStart(t *testing.T) {
	addr := startTestServer(t)
	comp := testComp(t, addr)
	startComp(t, comp)

	state := comp.Conn().GetState()
	if state != connectivity.Ready && state != connectivity.Idle {
		t.Fatalf("expected READY or IDLE after Start, got %s", state)
	}
}

func TestIntegration_RPCOverConn(t *testing.T) {
	// Verify the Conn returned by the component can actually make RPCs.
	addr := startHealthServer(t)
	comp := testComp(t, addr)
	startComp(t, comp)

	client := healthpb.NewHealthClient(comp.Conn())
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

func TestIntegration_AddOption_Interceptor(t *testing.T) {
	addr := startHealthServer(t)
	comp := testComp(t, addr)

	interceptorCalled := false
	comp.AddOption(grpclib.WithChainUnaryInterceptor(
		func(
			ctx context.Context,
			method string,
			req, reply any,
			cc *grpclib.ClientConn,
			invoker grpclib.UnaryInvoker,
			opts ...grpclib.CallOption,
		) error {
			interceptorCalled = true
			return invoker(ctx, method, req, reply, cc, opts...)
		},
	))

	startComp(t, comp)

	client := healthpb.NewHealthClient(comp.Conn())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health check RPC failed: %v", err)
	}
	if !interceptorCalled {
		t.Fatal("unary interceptor was not called")
	}
}

func TestIntegration_ServerStop_HealthDetectsFailure(t *testing.T) {
	// When the server shuts down, Health should eventually return an error
	// as the client detects the broken connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	srv := grpclib.NewServer()
	go func() { _ = srv.Serve(ln) }()

	comp := grpcclient.New(
		grpcclient.Config{Target: addr, ConnectTimeout: 5 * time.Second},
		grpcclient.WithLogger(&testLogger{t}),
	)
	startComp(t, comp)

	if err := comp.Health(context.Background()); err != nil {
		t.Fatalf("Health before server stop: %v", err)
	}

	// Hard-stop the server so the client connection breaks immediately.
	srv.Stop()

	// Poll Health until the client detects the failure.
	deadline := time.After(5 * time.Second)
	for {
		if err := comp.Health(context.Background()); err != nil {
			t.Logf("Health correctly returned error after server stop: %v", err)
			return
		}
		select {
		case <-deadline:
			t.Fatal("Health did not return error within deadline after server stopped")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestIntegration_MultipleClients_SameServer(t *testing.T) {
	addr := startHealthServer(t)

	comps := make([]*grpcclient.Component, 3)
	for i := range comps {
		comps[i] = grpcclient.New(
			grpcclient.Config{Target: addr, ConnectTimeout: 5 * time.Second},
			grpcclient.WithName(fmt.Sprintf("client-%d", i)),
			grpcclient.WithLogger(&testLogger{t}),
		)
		startComp(t, comps[i])
	}

	for i, comp := range comps {
		if err := comp.Health(context.Background()); err != nil {
			t.Errorf("client-%d Health: %v", i, err)
		}
		client := healthpb.NewHealthClient(comp.Conn())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, rpcErr := client.Check(ctx, &healthpb.HealthCheckRequest{})
		cancel()
		if rpcErr != nil {
			t.Errorf("client-%d RPC failed: %v", i, rpcErr)
		} else if resp.Status != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("client-%d expected SERVING, got %v", i, resp.Status)
		}
	}
}
