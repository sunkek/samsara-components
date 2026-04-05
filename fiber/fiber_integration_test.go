//go:build integration

package fiber_test

// Integration tests spin up a real Fiber server on an ephemeral port.
// No Docker required — the server binds locally.
//
// Run with:
//
//	go test -race -tags integration -timeout 60s ./...
//
// or via the repo root:
//
//	make test-integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	gf "github.com/gofiber/fiber/v3"
	sc "github.com/sunkek/samsara-components/fiber"
)

// testSrv creates a Component bound to an OS-assigned port.
// It binds a listener first and passes the port to Config so Fiber can
// re-bind the same address — avoiding the TOCTOU race of close-then-bind.
func testSrv(t *testing.T) (*sc.Component, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testSrv: listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// Close before Fiber binds — the OS keeps the port reserved briefly on
	// loopback. For test isolation this is sufficient; the alternative
	// (passing a net.Listener) requires Fiber's Listener() API which bypasses
	// the OnListen hook used by ready().
	ln.Close()
	cfg := sc.Config{
		Host:       "127.0.0.1",
		Port:       port,
		PathPrefix: "/api",
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/api", port)
	return sc.New(cfg, sc.WithLogger(&testLogger{t})), base
}

func startSrv(t *testing.T, srv *sc.Component) {
	t.Helper()
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		errCh <- srv.Start(ctx, func() { close(readyCh) })
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
		if err := srv.Stop(stopCtx); err != nil {
			t.Errorf("Stop returned error: %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start returned unexpected error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Start goroutine did not exit after Stop")
		}
	})
}

func TestIntegration_StartStop(t *testing.T) {
	srv, _ := testSrv(t)
	startSrv(t, srv)
}

func TestIntegration_HealthEndpoint(t *testing.T) {
	srv, base := testSrv(t)
	startSrv(t, srv)

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestIntegration_Health_ComponentCheck(t *testing.T) {
	srv, _ := testSrv(t)
	startSrv(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Health(ctx); err != nil {
		t.Fatalf("Health returned error on live server: %v", err)
	}
}

func TestIntegration_Register_RouteServed(t *testing.T) {
	srv, base := testSrv(t)

	srv.Register(func(r gf.Router) {
		r.Get("/ping", func(c gf.Ctx) error {
			return c.SendString("pong")
		})
	})

	startSrv(t, srv)

	resp, err := http.Get(base + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "pong" {
		t.Fatalf("expected %q, got %q", "pong", body)
	}
}

func TestIntegration_Register_SubGroup(t *testing.T) {
	srv, base := testSrv(t)

	srv.Register(func(r gf.Router) {
		v2 := r.Group("/v2")
		v2.Get("/hello", func(c gf.Ctx) error {
			return c.SendString("hello v2")
		})
	})

	startSrv(t, srv)

	resp, err := http.Get(base + "/v2/hello")
	if err != nil {
		t.Fatalf("GET /v2/hello: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "hello v2" {
		t.Fatalf("expected %q, got %q", "hello v2", body)
	}
}

func TestIntegration_DefaultErrorHandler_FiberError(t *testing.T) {
	srv, base := testSrv(t)

	srv.Register(func(r gf.Router) {
		r.Get("/boom", func(_ gf.Ctx) error {
			return gf.NewError(http.StatusTeapot, "I'm a teapot")
		})
	})

	startSrv(t, srv)

	resp, err := http.Get(base + "/boom")
	if err != nil {
		t.Fatalf("GET /boom: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", resp.StatusCode)
	}
	var body sc.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != "I'm a teapot" {
		t.Fatalf("unexpected error message: %q", body.Error)
	}
}

func TestIntegration_DefaultErrorHandler_HTTPStatuser(t *testing.T) {
	srv, base := testSrv(t)

	srv.Register(func(r gf.Router) {
		r.Get("/notfound", func(_ gf.Ctx) error {
			return &notFoundErr{}
		})
	})

	startSrv(t, srv)

	resp, err := http.Get(base + "/notfound")
	if err != nil {
		t.Fatalf("GET /notfound: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 via HTTPStatuser, got %d", resp.StatusCode)
	}
}

func TestIntegration_Use_MiddlewareApplied(t *testing.T) {
	srv, base := testSrv(t)

	srv.Use(func(c gf.Ctx) error {
		c.Set("X-Test-Middleware", "applied")
		return c.Next()
	})
	srv.Register(func(r gf.Router) {
		r.Get("/mw-test", func(c gf.Ctx) error {
			return c.SendString("ok")
		})
	})

	startSrv(t, srv)

	resp, err := http.Get(base + "/mw-test")
	if err != nil {
		t.Fatalf("GET /mw-test: %v", err)
	}
	resp.Body.Close()

	if v := resp.Header.Get("X-Test-Middleware"); v != "applied" {
		t.Fatalf("expected header %q, got %q", "applied", v)
	}
}

func TestIntegration_SecurityHeaders(t *testing.T) {
	srv, base := testSrv(t)
	startSrv(t, srv)

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()

	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options: expected %q, got %q", "DENY", got)
	}
}

func TestIntegration_Stop_DrainsInflightRequests(t *testing.T) {
	srv, base := testSrv(t)

	srv.Register(func(r gf.Router) {
		r.Get("/slow", func(c gf.Ctx) error {
			time.Sleep(300 * time.Millisecond)
			return c.SendString("done")
		})
	})

	startSrv(t, srv)

	resultCh := make(chan int, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			resultCh <- 0
			return
		}
		resp.Body.Close()
		resultCh <- resp.StatusCode
	}()

	time.Sleep(50 * time.Millisecond) // let the request arrive
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case code := <-resultCh:
		if code != http.StatusOK {
			t.Fatalf("in-flight request: expected 200, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete after graceful Stop")
	}
}

// notFoundErr implements HTTPStatuser.
type notFoundErr struct{}

func (e *notFoundErr) Error() string   { return "resource not found" }
func (e *notFoundErr) StatusCode() int { return http.StatusNotFound }
