// Package grpcclient provides a [github.com/sunkek/samsara]-compatible gRPC
// client component backed by [google.golang.org/grpc].
//
// # Usage
//
//	client := grpcclient.New(grpcclient.Config{
//	    Target: "localhost:9090",
//	})
//	sup.Add(client,
//	    samsara.WithTier(samsara.TierCritical),
//	    samsara.WithDependencies("grpc"),
//	)
//
//	// Pass the connection to generated stub constructors:
//	greeter := pb.NewGreeterClient(client.Conn())
//
// # Why manage the client lifecycle?
//
// [*grpc.ClientConn] holds a real connection pool. Managing it as a samsara
// component gives you:
//
//   - Ordered startup: the client connects only after its dependencies (e.g.
//     config, secrets) are ready.
//   - Ordered shutdown: the client connection drains before the services using
//     it are torn down.
//   - Health visibility: the supervisor monitors connectivity state and can
//     restart or flag the component accordingly.
//   - Consistent observability: the same logging and restart-policy hooks as
//     every other component.
//
// # Interceptors
//
// Use [Component.AddOption] to inject unary and stream interceptors before
// [samsara.Application.Run]:
//
//	client.AddOption(grpclib.WithChainUnaryInterceptor(authInterceptor))
//	client.AddOption(grpclib.WithChainStreamInterceptor(tracingInterceptor))
//
// # Connection target
//
// [Config.Target] accepts any target string supported by gRPC's name resolver,
// including plain host:port, dns:///host:port, or custom scheme resolvers.
// Plain TCP with no TLS is the default (consistent with the server component).
package grpcclient

import (
	"context"
	"fmt"
	"sync"

	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// Component is a samsara-compatible gRPC client component.
// Obtain one with [New]; register it with a samsara supervisor.
//
// Call [Component.Conn] to obtain the underlying [*grpc.ClientConn] and pass
// it to generated stub constructors. Conn is safe to call after Start.
//
// The typical wiring pattern in main.go:
//
//	client := grpcclient.New(cfg)
//	sup.Add(client, samsara.WithTier(samsara.TierCritical))
//
//	// After sup.Add, pass Conn() to the adapters that need it:
//	userAdapter := user.New(pb.NewUserServiceClient(client.Conn()))
type Component struct {
	cfg  Config
	log  Logger
	name string

	// mu guards conn and stopCh across the Start/Stop/restart lifecycle.
	mu     sync.RWMutex
	conn   *grpclib.ClientConn
	stopCh chan struct{}

	// optsMu guards the DialOptions (interceptors, etc.).
	optsMu sync.RWMutex
	opts   []grpclib.DialOption
}

// New creates a Component from the supplied config.
// The connection is not established until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "grpc-client",
		stopCh: make(chan struct{}), // initialised so Stop-before-Start is safe
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Option configures a [Component].
type Option func(*Component)

// WithLogger attaches a structured logger to the component.
// [log/slog.Logger] satisfies [Logger] directly.
func WithLogger(l Logger) Option {
	return func(c *Component) { c.log = l }
}

// WithName overrides the component name returned by [Component.Name].
// Useful when connecting to multiple backends with the same supervisor.
func WithName(name string) Option {
	return func(c *Component) { c.name = name }
}

// Compile-time assertion: *Component satisfies the samsara component and
// health-checker interfaces without importing the samsara package.
var (
	_ interface {
		Name() string
		Start(ctx context.Context, ready func()) error
		Stop(ctx context.Context) error
	} = (*Component)(nil)

	_ interface {
		Health(ctx context.Context) error
	} = (*Component)(nil)
)

// Name implements samsara.Component.
func (c *Component) Name() string { return c.name }

// AddOption appends a [grpclib.DialOption] (e.g. an interceptor chain) that
// will be applied when the connection is created during [Component.Start].
// AddOption must be called before Start — options added after Start have no
// effect on the running connection.
//
// Use this for unary and stream interceptors:
//
//	client.AddOption(grpclib.WithChainUnaryInterceptor(myInterceptor))
//	client.AddOption(grpclib.WithChainStreamInterceptor(myStreamInterceptor))
func (c *Component) AddOption(opt grpclib.DialOption) {
	c.optsMu.Lock()
	c.opts = append(c.opts, opt)
	c.optsMu.Unlock()
}

// Conn returns the underlying [*grpc.ClientConn].
// It is safe to call Conn before Start — the returned value will be nil until
// Start has been called. Callers that need the connection at startup should
// depend on this component via samsara.WithDependencies so that Start has
// already run by the time Conn is used.
func (c *Component) Conn() *grpclib.ClientConn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

// Start creates the client connection, waits for it to reach a ready state,
// calls ready(), then blocks until Stop or ctx cancellation.
//
// Start is safe to call multiple times across restarts; each call allocates a
// fresh connection and stopCh so the previous Stop signal does not bleed into
// the new run.
func (c *Component) Start(ctx context.Context, ready func()) error {
	// Allocate a fresh stopCh for this run under the write lock.
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	// Collect caller-supplied DialOptions under the read lock.
	c.optsMu.RLock()
	extraOpts := make([]grpclib.DialOption, len(c.opts))
	copy(extraOpts, c.opts)
	c.optsMu.RUnlock()

	// Build the full DialOption slice. Insecure transport goes first, then
	// config-derived options (message size limits, keepalive), then caller-
	// supplied options (interceptors, etc.) so callers can override defaults.
	// TLS is a future cross-cutting addition shared with the server component.
	base := append(
		[]grpclib.DialOption{grpclib.WithTransportCredentials(insecure.NewCredentials())},
		c.cfg.dialOptions()...,
	)
	dialOpts := append(base, extraOpts...)

	// grpc.NewClient is non-blocking — it does not dial until the first RPC
	// (or until WaitForStateChange forces it). We call it here to get the
	// conn object, then actively wait for READY below.
	conn, err := grpclib.NewClient(c.cfg.Target, dialOpts...)
	if err != nil {
		return fmt.Errorf("grpc-client: create connection to %q: %w", c.cfg.Target, err)
	}

	// Wait for the connection to reach READY within ConnectTimeout.
	// This gives the supervisor an accurate ready signal — not a speculative
	// "we created the object" signal. It also fails fast when the target is
	// genuinely unreachable, consistent with how postgresql and rabbitmq behave.
	connectCtx, cancel := context.WithTimeout(ctx, c.cfg.connectTimeout())
	defer cancel()

	// Trigger the first connection attempt by connecting proactively.
	conn.Connect()

	if err := waitReady(connectCtx, conn); err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			// Parent ctx cancelled — clean shutdown, not a failure.
			return nil
		}
		return fmt.Errorf("grpc-client: connect to %q: %w", c.cfg.Target, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	c.log.Info("grpc-client: connected", "target", c.cfg.Target)
	ready()

	select {
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// Stop closes the client connection within the context deadline.
// It is idempotent and concurrency-safe.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	// Replace stopCh with a pre-closed channel so subsequent Stop calls
	// and any future Start that races with this Stop see a consistent state.
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	// Signal the currently-running Start (if any) to exit.
	select {
	case <-ch:
		// Already closed — Stop called before Start, or called twice.
	default:
		close(ch)
	}

	if conn == nil {
		return nil // Stop called before Start — nothing to do
	}

	done := make(chan struct{})
	go func() {
		_ = conn.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		c.log.Error("grpc-client: connection close timed out during shutdown")
	}
	return nil
}

// Health implements samsara.HealthChecker.
// Returns a non-nil error if the connection is not in READY or IDLE state.
// IDLE is healthy because gRPC clients re-enter IDLE after a period of
// inactivity and reconnect automatically on the next RPC.
func (c *Component) Health(_ context.Context) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("grpc-client: connection not initialised")
	}

	state := conn.GetState()
	switch state {
	case connectivity.Ready, connectivity.Idle:
		return nil
	default:
		return fmt.Errorf("grpc-client: connection state is %s", state)
	}
}

// waitReady blocks until conn reaches READY state or the ctx is cancelled.
// It loops through WaitForStateChange so it reacts to each connectivity
// transition rather than polling on a fixed interval.
func waitReady(ctx context.Context, conn *grpclib.ClientConn) error {
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.TransientFailure || state == connectivity.Shutdown {
			// Wait for this bad state to change (i.e. a retry attempt starts)
			// rather than bailing immediately — gRPC retries automatically.
			// If the ctx expires while we wait, the select below handles it.
		}
		if !conn.WaitForStateChange(ctx, state) {
			// ctx expired before the state changed.
			return fmt.Errorf("timed out in state %s", state)
		}
	}
}
