package fiber_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	gf "github.com/gofiber/fiber/v3"
	sc "github.com/sunkek/samsara-components/fiber"
)

// ----------------------------------------------------------------------------
// Construction
// ----------------------------------------------------------------------------

func TestNew_DefaultName(t *testing.T) {
	srv := sc.New(sc.Config{})
	if srv.Name() != "fiber" {
		t.Fatalf("expected name %q, got %q", "fiber", srv.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	srv := sc.New(sc.Config{}, sc.WithName("api-server"))
	if srv.Name() != "api-server" {
		t.Fatalf("expected %q, got %q", "api-server", srv.Name())
	}
}

func TestNew_WithLogger(t *testing.T) {
	srv := sc.New(sc.Config{}, sc.WithLogger(&testLogger{t}))
	if srv == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// Lifecycle (no server binding needed)
// ----------------------------------------------------------------------------

func TestStop_BeforeStart(t *testing.T) {
	srv := sc.New(sc.Config{})
	done := make(chan error, 1)
	go func() { done <- srv.Stop(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked unexpectedly before Start")
	}
}

func TestHealth_BeforeStart(t *testing.T) {
	srv := sc.New(sc.Config{})
	if err := srv.Health(context.Background()); err == nil {
		t.Fatal("expected error from Health before Start")
	}
}

// ----------------------------------------------------------------------------
// Register + Use (no binding — verify storage without panic)
// ----------------------------------------------------------------------------

func TestRegister_BeforeStart(t *testing.T) {
	srv := sc.New(sc.Config{})
	srv.Register(func(r gf.Router) {
		// intentionally empty — just verifies the call doesn't panic
	})
}

func TestUse_BeforeStart(t *testing.T) {
	srv := sc.New(sc.Config{})
	// Store a no-op middleware; must not panic.
	srv.Use(func(c gf.Ctx) error { return c.Next() })
}

// ----------------------------------------------------------------------------
// Config
// ----------------------------------------------------------------------------

func TestConfig_ZeroValueNoPanic(t *testing.T) {
	srv := sc.New(sc.Config{})
	if srv == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// DefaultErrorHandler
// ----------------------------------------------------------------------------

func TestDefaultErrorHandler_FiberError(t *testing.T) {
	// Verify DefaultErrorHandler is exported and callable.
	_ = sc.DefaultErrorHandler
}

func TestErrorResponse_Fields(t *testing.T) {
	r := sc.ErrorResponse{Error: "something went wrong"}
	if r.Error != "something went wrong" {
		t.Fatalf("unexpected: %q", r.Error)
	}
}

// ----------------------------------------------------------------------------
// HTTPStatuser interface
// ----------------------------------------------------------------------------

// customErr implements HTTPStatuser.
type customErr struct{ code int }

func (e *customErr) Error() string   { return "custom error" }
func (e *customErr) StatusCode() int { return e.code }

func TestHTTPStatuser_Interface(t *testing.T) {
	var _ sc.HTTPStatuser = (*customErr)(nil)
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func TestRealIP_Exported(t *testing.T) {
	_ = sc.RealIP // compile-time export check
}

func TestExcludeRoutes_Logic(t *testing.T) {
	skipper := sc.ExcludeRoutes(
		sc.Route{Method: http.MethodGet, Path: "/api/health"},
	)
	if skipper == nil {
		t.Fatal("expected non-nil SkipperFunc")
	}
}

func TestRoute_Fields(t *testing.T) {
	r := sc.Route{Method: "GET", Path: "/health"}
	if r.Method != "GET" || r.Path != "/health" {
		t.Fatalf("unexpected Route: %+v", r)
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type testLogger struct{ t *testing.T }

func (l *testLogger) Info(msg string, args ...any)  { l.t.Log(append([]any{"INFO ", msg}, args...)...) }
func (l *testLogger) Error(msg string, args ...any) { l.t.Log(append([]any{"ERROR", msg}, args...)...) }
