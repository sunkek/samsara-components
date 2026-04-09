// Package fiber provides a [github.com/sunkek/samsara]-compatible HTTP server
// component backed by [Fiber v3].
//
// # Usage
//
//	srv := fiber.New(fiber.Config{
//	    Host: "0.0.0.0",
//	    Port: 8080,
//	})
//
//	// Register domain routes before app.Run():
//	srv.Register(func(r gf.Router) {
//	    r.Get("/users",  handleGetUsers)
//	    r.Post("/users", handleCreateUser)
//
//	    // Sub-groups work naturally:
//	    admin := r.Group("/admin", adminMiddleware)
//	    admin.Get("/stats", handleStats)
//	})
//
//	sup.Add(srv, samsara.WithTier(samsara.TierCritical))
//
// The component calls ready() as soon as the TCP port is bound, giving the
// supervisor a precise "port is open" signal before any request can arrive.
package fiber

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	gf "github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

// Config holds all configuration for the Fiber HTTP server component.
type Config struct {
	// Host is the listen address. Defaults to "0.0.0.0".
	Host string
	// Port is the listen port. Defaults to 8080.
	Port int

	// PathPrefix is the URL prefix for all registered routes.
	// Defaults to "/api/v1".
	PathPrefix string

	// BodyLimitMB is the maximum request body size in megabytes. Defaults to 4.
	BodyLimitMB int

	// CORSAllowOrigins is the list of allowed CORS origins.
	// Defaults to ["*"].
	CORSAllowOrigins []string
	// CORSAllowMethods is the list of allowed CORS methods.
	// Defaults to ["GET","POST","PUT","PATCH","DELETE","OPTIONS"].
	CORSAllowMethods []string
	// CORSAllowHeaders is the list of allowed CORS headers.
	// Defaults to ["*"].
	CORSAllowHeaders []string

	// ReadTimeout is the maximum duration for reading the entire request.
	// Defaults to 5 s.
	ReadTimeout time.Duration
	// WriteTimeout is the maximum duration for writing the response.
	// Defaults to 10 s.
	WriteTimeout time.Duration
	// IdleTimeout is the maximum duration to wait for the next request.
	// Defaults to 30 s.
	IdleTimeout time.Duration

	// ErrorHandler is called when a route handler returns a non-nil error.
	// If nil, a default JSON error handler is used (see [DefaultErrorHandler]).
	ErrorHandler gf.ErrorHandler

	// LoggerFormat is the format string for the request logger middleware.
	// If empty, a structured JSON format is used. Set to "-" to disable.
	LoggerFormat string

	// EnableSecurityHeaders adds security-related response headers (HSTS,
	// X-Frame-Options, CORP/COEP). Defaults to true.
	EnableSecurityHeaders *bool
}

func (c Config) addr() string {
	host := c.Host
	if host == "" {
		host = "0.0.0.0"
	}
	port := c.Port
	if port == 0 {
		port = 8080
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func (c Config) pathPrefix() string {
	if c.PathPrefix != "" {
		return c.PathPrefix
	}
	return "/api/v1"
}

func (c Config) bodyLimitBytes() int {
	if c.BodyLimitMB > 0 {
		return c.BodyLimitMB * 1024 * 1024
	}
	return 4 * 1024 * 1024
}

func (c Config) corsOrigins() []string {
	if len(c.CORSAllowOrigins) > 0 {
		return c.CORSAllowOrigins
	}
	return []string{"*"}
}

func (c Config) corsMethods() []string {
	if len(c.CORSAllowMethods) > 0 {
		return c.CORSAllowMethods
	}
	return []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
}

func (c Config) corsHeaders() []string {
	if len(c.CORSAllowHeaders) > 0 {
		return c.CORSAllowHeaders
	}
	return []string{"*"}
}

func (c Config) loggerFormat() string {
	if c.LoggerFormat != "" {
		return c.LoggerFormat
	}
	return `{"time":"${time}","ip":"${ip}","x-forwarded-for":"${reqHeader:X-Forwarded-For}","status":${status},"latency":"${latency}","method":"${method}","path":"${path}","error":"${error}"}` + "\n"
}

func (c Config) securityHeaders() bool {
	if c.EnableSecurityHeaders != nil {
		return *c.EnableSecurityHeaders
	}
	return true
}

func (c Config) errorHandler() gf.ErrorHandler {
	if c.ErrorHandler != nil {
		return c.ErrorHandler
	}
	return DefaultErrorHandler
}

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// RegisterFunc is a callback that receives the root [gf.Router] (scoped to
// [Config.PathPrefix]) and registers routes and sub-group middleware on it.
// All RegisterFuncs are called in registration order during [Component.Start],
// after the built-in middleware stack is applied.
type RegisterFunc func(r gf.Router)

// Component is a samsara-compatible Fiber HTTP server component.
// Obtain one with [New]; register it with a samsara supervisor.
//
// Call [Component.Register] to add domain routes before [samsara.Application.Run].
// The component is designed to be used as follows in main.go:
//
//	srv := fiber.New(cfg)
//	userAdapter.RegisterRoutes(srv)  // calls srv.Register(...)
//	sup.Add(srv)
type Component struct {
	cfg  Config
	log  Logger
	name string

	// mu guards app and listening across Start/Stop.
	mu        sync.RWMutex
	app       *gf.App
	listening bool

	// routes holds RegisterFuncs to be called during Start.
	routesMu sync.RWMutex
	routes   []RegisterFunc

	// middleware holds Use() calls to apply before domain routes.
	middlewareMu sync.RWMutex
	middleware   []any
}

// New creates a Component from the supplied config.
// The HTTP server is not started until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:  cfg,
		log:  nopLogger{},
		name: "fiber",
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
func WithName(name string) Option {
	return func(c *Component) { c.name = name }
}

// healthClient is used by [Component.Health] to probe the built-in /health
// endpoint. A dedicated client with a fixed timeout avoids inheriting
// http.DefaultClient's no-timeout default, which would block Health
// indefinitely if the server is wedged and the caller passes context.Background.
var healthClient = &http.Client{Timeout: 5 * time.Second}

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

// Start initialises the Fiber app, applies middleware, calls all registered
// [RegisterFunc]s, then begins serving. Calls ready() the moment the TCP
// port is bound — before any request can be accepted.
//
// Start is safe to call multiple times across restarts.
func (c *Component) Start(ctx context.Context, ready func()) error {
	app := gf.New(gf.Config{
		BodyLimit:    c.cfg.bodyLimitBytes(),
		ReadTimeout:  c.cfg.ReadTimeout,
		WriteTimeout: c.cfg.WriteTimeout,
		IdleTimeout:  c.cfg.IdleTimeout,
		ErrorHandler: c.cfg.errorHandler(),
	})

	// ── Built-in middleware ────────────────────────────────────────────────
	app.Use(recover.New(recover.Config{EnableStackTrace: true}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: c.cfg.corsOrigins(),
		AllowMethods: c.cfg.corsMethods(),
		AllowHeaders: c.cfg.corsHeaders(),
	}))
	if c.cfg.securityHeaders() {
		app.Use(securityHeadersMiddleware)
	}
	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))

	// ── Caller-supplied global middleware ─────────────────────────────────
	// Applied before domain routes but after built-in middleware, so callers
	// can inject auth, tracing, etc. that wraps all domain handlers.
	c.middlewareMu.RLock()
	mw := make([]any, len(c.middleware))
	copy(mw, c.middleware)
	c.middlewareMu.RUnlock()

	if len(mw) > 0 {
		app.Use(mw...)
	}

	// ── Root group (PathPrefix) ────────────────────────────────────────────
	root := app.Group(c.cfg.pathPrefix())

	// Built-in /health endpoint for orchestrator probes. Registered before
	// the logger middleware so probe traffic is not logged.
	root.Get("/health", func(c gf.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})

	if c.cfg.LoggerFormat != "-" {
		root.Use(logger.New(logger.Config{
			Format:     c.cfg.loggerFormat(),
			TimeFormat: time.RFC3339,
		}))
	}

	// ── Domain routes ──────────────────────────────────────────────────────
	// Call each RegisterFunc with the root router. Funcs are called in
	// registration order so dependency ordering is preserved.
	c.routesMu.RLock()
	fns := make([]RegisterFunc, len(c.routes))
	copy(fns, c.routes)
	c.routesMu.RUnlock()

	for _, fn := range fns {
		fn(root)
	}

	// Store app before Listen so Stop can call ShutdownWithContext even if
	// ctx is cancelled between now and the OnListen hook firing.
	c.mu.Lock()
	c.app = app
	c.mu.Unlock()

	// Watch ctx so a samsara context cancellation triggers a graceful
	// shutdown even if Stop is not called explicitly (e.g. supervisor
	// timeout path). This mirrors the rabbitmq/postgresql pattern.
	go func() {
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = app.ShutdownWithContext(stopCtx)
	}()

	// Fiber's OnListen hook fires after the TCP socket is bound, before the
	// first Accept — giving the supervisor a precise "port is open" signal.
	app.Hooks().OnListen(func(_ gf.ListenData) error {
		c.mu.Lock()
		c.listening = true
		c.mu.Unlock()
		ready()
		return nil
	})

	if err := app.Listen(c.cfg.addr(), gf.ListenConfig{
		DisableStartupMessage: true,
	}); err != nil {
		return fmt.Errorf("fiber: listen: %w", err)
	}

	c.mu.Lock()
	c.listening = false
	c.app = nil // clear so Stop on a finished app is a no-op
	c.mu.Unlock()

	return nil
}

// Stop gracefully shuts down the HTTP server, draining in-flight requests
// within the context deadline.
func (c *Component) Stop(ctx context.Context) error {
	c.mu.RLock()
	app := c.app
	c.mu.RUnlock()

	if app == nil {
		return nil // Stop called before Start — nothing to do
	}
	return app.ShutdownWithContext(ctx)
}

// Health implements samsara.HealthChecker.
// Returns a non-nil error if the server is not currently listening.
func (c *Component) Health(ctx context.Context) error {
	c.mu.RLock()
	listening := c.listening
	addr := c.cfg.addr()
	prefix := c.cfg.pathPrefix()
	c.mu.RUnlock()

	if !listening {
		return fmt.Errorf("fiber: server is not listening")
	}

	// Probe the built-in /health endpoint to verify end-to-end reachability.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+addr+prefix+"/health", nil)
	if err != nil {
		return fmt.Errorf("fiber: health probe: %w", err)
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return fmt.Errorf("fiber: health probe: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("fiber: health probe returned %d", resp.StatusCode)
	}
	return nil
}

// securityHeadersMiddleware adds hardened HTTP security headers to every
// response. Applied when [Config.EnableSecurityHeaders] is true (default).
func securityHeadersMiddleware(c gf.Ctx) error {
	// HSTS: instruct browsers to use HTTPS exclusively for 2 years.
	c.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
	// Prevent the page from being embedded in an iframe (clickjacking).
	c.Set("X-Frame-Options", "DENY")
	// Isolate the browsing context so cross-origin documents cannot access it.
	c.Set("Cross-Origin-Opener-Policy", "same-origin")
	// Require CORS for sub-resources (enables SharedArrayBuffer etc.).
	c.Set("Cross-Origin-Embedder-Policy", "require-corp")
	return c.Next()
}
