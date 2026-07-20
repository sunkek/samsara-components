package prometheus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// freePort grabs an ephemeral port that is free at call time.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startComponent runs c.Start in a goroutine and waits for ready.
func startComponent(t *testing.T, c *Component) chan error {
	t.Helper()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- c.Start(context.Background(), func() { close(ready) }) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Start returned before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for ready")
	}
	return done
}

func scrape(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestServeAndScrape(t *testing.T) {
	port := freePort(t)
	c := New(Config{Host: "127.0.0.1", Port: port})
	done := startComponent(t, c)

	body := scrape(t, fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
	if !strings.Contains(body, "go_goroutines") {
		t.Error("expected runtime collector metrics in scrape output")
	}

	if err := c.Health(context.Background()); err != nil {
		t.Errorf("Health while running: %v", err)
	}

	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

func TestObserverMetrics(t *testing.T) {
	port := freePort(t)
	c := New(Config{Host: "127.0.0.1", Port: port, DisableRuntimeCollectors: true})
	o := c.Observer()

	o.ComponentStarted("db", 1)
	o.ComponentRestarting("db", errors.New("boom"), 1, time.Second)
	o.ComponentStopped("db", errors.New("close failed"))
	o.HealthCheckCompleted("db", 50*time.Millisecond, errors.New("unhealthy"))

	done := startComponent(t, c)
	defer func() {
		_ = c.Stop(context.Background())
		<-done
	}()

	body := scrape(t, fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
	for _, want := range []string{
		`samsara_component_up{component="db"} 0`,
		`samsara_component_starts_total{component="db"} 1`,
		`samsara_component_restarts_total{component="db"} 1`,
		`samsara_component_stop_errors_total{component="db"} 1`,
		`samsara_component_health_check_failures_total{component="db"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape output missing %q", want)
		}
	}
}

func TestStopBeforeStart(t *testing.T) {
	c := New(Config{})
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	if err := c.Health(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Health before Start = %v, want ErrNotReady", err)
	}
}

func TestRestart(t *testing.T) {
	port := freePort(t)
	c := New(Config{Host: "127.0.0.1", Port: port})

	for i := 0; i < 2; i++ {
		done := startComponent(t, c)
		scrape(t, fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
		if err := c.Stop(context.Background()); err != nil {
			t.Fatalf("run %d Stop: %v", i, err)
		}
		if err := <-done; err != nil {
			t.Fatalf("run %d Start: %v", i, err)
		}
	}
}

func TestListenConflict(t *testing.T) {
	port := freePort(t)
	c1 := New(Config{Host: "127.0.0.1", Port: port})
	done := startComponent(t, c1)
	defer func() {
		_ = c1.Stop(context.Background())
		<-done
	}()

	c2 := New(Config{Host: "127.0.0.1", Port: port})
	err := c2.Start(context.Background(), func() {})
	if err == nil {
		t.Fatal("expected listen error on occupied port")
	}
}

func TestCustomPathAndName(t *testing.T) {
	port := freePort(t)
	c := New(Config{Host: "127.0.0.1", Port: port, Path: "/m", DisableRuntimeCollectors: true},
		WithName("metrics2"))
	if c.Name() != "metrics2" {
		t.Fatalf("Name = %q", c.Name())
	}
	done := startComponent(t, c)
	defer func() {
		_ = c.Stop(context.Background())
		<-done
	}()

	scrape(t, fmt.Sprintf("http://127.0.0.1:%d/m", port))

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("default path status = %d, want 404", resp.StatusCode)
	}
}
