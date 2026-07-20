// Package prometheus provides a [github.com/sunkek/samsara]-compatible
// metrics component backed by [prometheus/client_golang].
//
// It bundles two things:
//
//   - Component — an HTTP server exposing a Prometheus registry in
//     exposition format (default :2112/metrics), supervised like any
//     other samsara component.
//   - Observer — a samsara.MetricsObserver implementation (structural,
//     no samsara import) that bridges supervisor telemetry — component
//     up/down, restarts, health-check latency — into that registry.
//
// # Usage
//
//	m := prometheus.New(prometheus.Config{Port: 2112})
//	sup := samsara.NewSupervisor(
//	    samsara.WithMetricsObserver(m.Observer()),
//	)
//	sup.Add(m, samsara.WithTier(samsara.TierAuxiliary))
//
// Application metrics register on the same registry:
//
//	requests := prometheus.NewCounterVec(...)
//	m.Registry().MustRegister(requests)
package prometheus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ErrNotReady is returned by Health when the component has not been started.
var ErrNotReady = errors.New("prometheus: not started")

// Config holds all parameters for the metrics endpoint.
type Config struct {
	// Host is the interface to bind. Defaults to "" (all interfaces).
	Host string
	// Port is the listen port. Defaults to 2112.
	Port int
	// Path is the URL path serving the exposition format.
	// Defaults to "/metrics".
	Path string

	// ReadTimeout bounds reading a request, including the body.
	// Defaults to 5 s.
	ReadTimeout time.Duration
	// WriteTimeout bounds writing a response. Scrapes of very large
	// registries must complete within it. Defaults to 30 s.
	WriteTimeout time.Duration

	// DisableRuntimeCollectors skips registering the Go runtime and
	// process collectors on the default registry. Ignored when a custom
	// registry is supplied via WithRegistry.
	DisableRuntimeCollectors bool
}

func (c Config) port() int {
	if c.Port == 0 {
		return 2112
	}
	return c.Port
}

func (c Config) path() string {
	if c.Path == "" {
		return "/metrics"
	}
	return c.Path
}

func (c Config) addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.port())
}

func (c Config) readTimeout() time.Duration {
	if c.ReadTimeout <= 0 {
		return 5 * time.Second
	}
	return c.ReadTimeout
}

func (c Config) writeTimeout() time.Duration {
	if c.WriteTimeout <= 0 {
		return 30 * time.Second
	}
	return c.WriteTimeout
}

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// Component is a samsara-compatible Prometheus metrics endpoint.
// Obtain one with [New]; register it with a samsara supervisor.
type Component struct {
	cfg      Config
	log      Logger
	name     string
	registry *prometheus.Registry
	observer *Observer

	// mu guards server and stopCh across Start/Stop/restart.
	mu     sync.RWMutex
	server *http.Server
	stopCh chan struct{}
}

// New creates a Component from the supplied config.
// The endpoint does not listen until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "prometheus",
		stopCh: make(chan struct{}), // initialised so Stop-before-Start is safe
	}
	for _, o := range opts {
		o(c)
	}
	if c.registry == nil {
		c.registry = prometheus.NewRegistry()
		if !cfg.DisableRuntimeCollectors {
			c.registry.MustRegister(
				collectors.NewGoCollector(),
				collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
			)
		}
	}
	c.observer = newObserver(c.registry)
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
func WithName(name string) Option {
	return func(c *Component) { c.name = name }
}

// WithRegistry serves the supplied registry instead of creating one.
// The caller is responsible for registering runtime collectors.
func WithRegistry(reg *prometheus.Registry) Option {
	return func(c *Component) { c.registry = reg }
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

// Registry returns the registry served by the endpoint. Application
// collectors should be registered on it before scraping begins.
func (c *Component) Registry() *prometheus.Registry { return c.registry }

// Observer returns the supervisor-telemetry bridge for
// samsara.WithMetricsObserver. Safe to call before Start.
func (c *Component) Observer() *Observer { return c.observer }

// Handler returns an http.Handler serving the registry, for embedding the
// endpoint into an existing server instead of running this component.
func (c *Component) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

// Start binds the listener, calls ready() to unblock the supervisor, then
// blocks serving scrapes until Stop or ctx cancellation.
//
// Start is safe to call multiple times across restarts.
func (c *Component) Start(ctx context.Context, ready func()) error {
	mux := http.NewServeMux()
	mux.Handle(c.cfg.path(), c.Handler())

	srv := &http.Server{
		Addr:         c.cfg.addr(),
		Handler:      mux,
		ReadTimeout:  c.cfg.readTimeout(),
		WriteTimeout: c.cfg.writeTimeout(),
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("prometheus: listen %s: %w", srv.Addr, err)
	}

	c.mu.Lock()
	c.server = srv
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	c.log.Info("prometheus: serving metrics", "addr", srv.Addr, "path", c.cfg.path())
	ready()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("prometheus: serve: %w", err)
		}
		return nil
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// Stop gracefully shuts the server down, letting in-flight scrapes finish
// until ctx expires. It is idempotent and concurrency-safe.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	srv := c.server
	c.server = nil
	c.mu.Unlock()

	select {
	case <-ch:
	default:
		close(ch)
	}

	if srv == nil {
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			c.log.Warn("prometheus: graceful shutdown timed out; closing")
			return srv.Close()
		}
		return fmt.Errorf("prometheus: shutdown: %w", err)
	}
	return nil
}

// Health implements samsara.HealthChecker. Returns a non-nil error when the
// server is not running.
func (c *Component) Health(_ context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.server == nil {
		return ErrNotReady
	}
	return nil
}
