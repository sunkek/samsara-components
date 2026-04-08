// Package grpc provides a [github.com/sunkek/samsara]-compatible gRPC server
// component backed by [google.golang.org/grpc].
//
// # Usage
//
//	srv := grpc.New(grpc.Config{
//	    Host: "0.0.0.0",
//	    Port: 9090,
//	})
//
//	// Register generated service implementations before app.Run():
//	srv.Register(func(s *grpclib.Server) {
//	    pb.RegisterMyServiceServer(s, &myServiceImpl{})
//	})
//
//	sup.Add(srv, samsara.WithTier(samsara.TierCritical))
//
// The component calls ready() as soon as the TCP port is bound — before
// any RPC can be accepted — giving the supervisor a precise startup signal.
//
// # Interceptors
//
// Use [Component.AddOption] to inject unary and stream interceptors (for
// auth, logging, tracing, etc.) before [samsara.Application.Run]:
//
//	srv.AddOption(grpclib.ChainUnaryInterceptor(authInterceptor, loggingInterceptor))
//	srv.AddOption(grpclib.ChainStreamInterceptor(authStreamInterceptor))
//
// # Reflection
//
// Set [Config.EnableReflection] to true in development or staging environments
// to allow tools like grpcurl to introspect the server without proto files.
// Keep it off (the default) in production — reflection exposes your full API
// surface to anyone who can reach the port.
package grpc

import (
	"context"
	"fmt"
	"net"
	"sync"

	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// RegisterFunc is a callback that receives the live [*grpc.Server] and
// registers one or more generated service implementations on it.
// All RegisterFuncs are called in registration order during [Component.Start].
//
// Example:
//
//	srv.Register(func(s *grpclib.Server) {
//	    pb.RegisterGreeterServer(s, &greeterImpl{})
//	    pb.RegisterUserServiceServer(s, &userImpl{})
//	})
type RegisterFunc func(s *grpclib.Server)

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// Component is a samsara-compatible gRPC server component.
// Obtain one with [New]; register it with a samsara supervisor.
//
// Call [Component.Register] to add service implementations before
// [samsara.Application.Run]. The component is typically wired like this:
//
//	srv := grpc.New(cfg)
//	myAdapter.RegisterGRPC(srv)  // calls srv.Register(...)
//	sup.Add(srv)
type Component struct {
	cfg  Config
	log  Logger
	name string

	// mu guards server and serving across Start/Stop.
	mu      sync.RWMutex
	server  *grpclib.Server
	serving bool

	// regsMu guards the RegisterFuncs slice. Separate from mu so that
	// Register() calls do not need to acquire the broad server lock.
	regsMu sync.RWMutex
	regs   []RegisterFunc

	// optsMu guards the extra ServerOptions (interceptors, etc.).
	optsMu sync.RWMutex
	opts   []grpclib.ServerOption

	// stopCh is initialised in New so Stop-before-Start is safe.
	stopCh chan struct{}
}

// New creates a Component from the supplied config.
// The gRPC server is not started until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "grpc",
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
// Useful when registering multiple gRPC servers with the same supervisor.
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

// Register adds a [RegisterFunc] that will be called during [Component.Start]
// to register service implementations on the live *grpc.Server.
// Register is safe to call before or after Start.
func (c *Component) Register(fn RegisterFunc) {
	c.regsMu.Lock()
	c.regs = append(c.regs, fn)
	c.regsMu.Unlock()
}

// AddOption appends a [grpclib.ServerOption] (e.g. an interceptor chain) that
// will be applied when the server is created during [Component.Start].
// AddOption must be called before Start — options added after Start have no
// effect on the running server.
//
// Use this for unary and stream interceptors:
//
//	srv.AddOption(grpclib.ChainUnaryInterceptor(myInterceptor))
//	srv.AddOption(grpclib.ChainStreamInterceptor(myStreamInterceptor))
func (c *Component) AddOption(opt grpclib.ServerOption) {
	c.optsMu.Lock()
	c.opts = append(c.opts, opt)
	c.optsMu.Unlock()
}

// Start binds the TCP port, creates the gRPC server, registers all services,
// calls ready(), then begins serving. ready() is called the moment the port is
// bound — before any RPC can be accepted.
//
// Start is safe to call multiple times across restarts; each call allocates a
// fresh stopCh so the previous Stop signal does not bleed into the new run.
func (c *Component) Start(ctx context.Context, ready func()) error {
	// Allocate a fresh stopCh for this run under the write lock, so a
	// concurrent Stop always operates on a valid, current channel.
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	// Bind the port first. This is the precise moment the server is
	// "reachable" — ready() fires right after, giving the supervisor
	// an accurate signal rather than a speculative "about to listen".
	ln, err := net.Listen("tcp", c.cfg.addr())
	if err != nil {
		return fmt.Errorf("grpc: listen %s: %w", c.cfg.addr(), err)
	}

	// Build ServerOptions: caller-supplied options first, then keepalive
	// policy so that callers cannot accidentally override safety defaults.
	c.optsMu.RLock()
	extraOpts := make([]grpclib.ServerOption, len(c.opts))
	copy(extraOpts, c.opts)
	c.optsMu.RUnlock()

	serverOpts := append(extraOpts, c.cfg.keepaliveOptions()...)
	srv := grpclib.NewServer(serverOpts...)

	// Register the gRPC health service. This allows orchestrators (Kubernetes
	// liveness/readiness probes, load balancers) to query health via the
	// standard gRPC health protocol rather than requiring a sidecar.
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	// Optionally register the reflection service (dev/staging only).
	// Reflection lets grpcurl and similar tools introspect the server's API
	// without access to the .proto files. Keep disabled in production.
	if c.cfg.EnableReflection {
		reflection.Register(srv)
		c.log.Info("grpc: reflection enabled")
	}

	// Call all registered service RegisterFuncs in order.
	c.regsMu.RLock()
	fns := make([]RegisterFunc, len(c.regs))
	copy(fns, c.regs)
	c.regsMu.RUnlock()

	for _, fn := range fns {
		fn(srv)
	}

	c.mu.Lock()
	c.server = srv
	c.serving = true
	c.mu.Unlock()

	c.log.Info("grpc: listening", "addr", c.cfg.addr())

	// Port is bound and server is fully configured — signal the supervisor.
	// This mirrors Fiber's OnListen hook: the port is open, all services are
	// registered, ready to accept RPCs.
	ready()

	// Watch ctx so a samsara context cancellation triggers a graceful
	// stop even if Stop is not called explicitly (e.g. supervisor timeout).
	go func() {
		<-ctx.Done()
		// Mark serving=false before GracefulStop so that when Serve()
		// returns its "closed network connection" error we treat it as a
		// clean exit rather than a crash. This mirrors what Stop() does.
		c.mu.Lock()
		c.serving = false
		c.mu.Unlock()
		srv.GracefulStop()
	}()

	// Serve blocks until GracefulStop or Stop is called.
	if err := srv.Serve(ln); err != nil {
		// GracefulStop closes the listener, which causes Serve to return
		// a "use of closed network connection" error — that is a clean
		// shutdown, not a failure. grpc.Server does not export a sentinel
		// for this, but it does set serving=false before Serve returns,
		// so we check our own flag set in Stop.
		c.mu.RLock()
		serving := c.serving
		c.mu.RUnlock()
		if serving {
			// serving is still true means Stop was not called — the error
			// is unexpected. Return it so the supervisor can restart.
			return fmt.Errorf("grpc: serve: %w", err)
		}
		// serving=false: Stop was called, GracefulStop closed the listener.
		// Treat as clean exit.
	}

	select {
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// Stop gracefully drains in-flight RPCs and shuts down the server within the
// context deadline. It is idempotent and concurrency-safe.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	// Replace stopCh with a pre-closed channel so subsequent Stop calls
	// and any future Start that races with this Stop see a consistent state.
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	srv := c.server
	c.serving = false
	c.mu.Unlock()

	// Signal the currently-running Start (if any) to exit.
	select {
	case <-ch:
		// Already closed — Stop called before Start, or called twice.
	default:
		close(ch)
	}

	if srv == nil {
		return nil // Stop called before Start — nothing to do
	}

	// GracefulStop drains in-flight RPCs then closes the listener.
	// We run it in a goroutine so we can honour the ctx deadline.
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		// Deadline exceeded — force-stop to avoid blocking the supervisor.
		c.log.Error("grpc: graceful stop timed out, forcing stop")
		srv.Stop()
		<-done
	}
	return nil
}

// Health implements samsara.HealthChecker.
// Returns a non-nil error if the server is not currently serving.
func (c *Component) Health(_ context.Context) error {
	c.mu.RLock()
	serving := c.serving
	addr := c.cfg.addr()
	c.mu.RUnlock()

	if !serving {
		return fmt.Errorf("grpc: server is not serving on %s", addr)
	}
	return nil
}
