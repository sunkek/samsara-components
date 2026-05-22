//go:build integration

package redis_test

// Integration tests require a running Redis instance.
// Start one with: make infra-up (from the repo root).
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
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/redis"
)

const testAddr = "localhost:6379" // matches docker-compose.yml (no auth)

func testComp(t *testing.T) *redis.Component {
	t.Helper()
	return redis.New(
		redis.Config{Host: "localhost", Port: 6379, ConnectTimeout: 10 * time.Second},
		redis.WithLogger(&testLogger{t}),
	)
}

func startComp(t *testing.T, comp *redis.Component) {
	t.Helper()
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() { errCh <- comp.Start(ctx, func() { close(readyCh) }) }()

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
				t.Errorf("Start returned unexpected error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Start goroutine did not exit after Stop")
		}
	})
}

// uniqueKey returns a key namespaced to this test run to avoid cross-test pollution.
func uniqueKey(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("_sc_redis_test:%s:%s", t.Name(), suffix)
}

func TestIntegration_StartStop(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
}

func TestIntegration_Health(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := comp.Health(ctx); err != nil {
		t.Fatalf("Health returned error on live server: %v", err)
	}
}

func TestIntegration_Restart(t *testing.T) {
	comp := testComp(t)
	for i := range 2 {
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

func TestIntegration_SetGet(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()
	key := uniqueKey(t, "k1")
	t.Cleanup(func() { _, _ = comp.Del(context.Background(), key) })

	if err := comp.Set(ctx, key, "hello", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, err := comp.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "hello" {
		t.Fatalf("expected %q, got %q", "hello", val)
	}
}

func TestIntegration_Get_ErrNil(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	_, err := comp.Get(ctx, uniqueKey(t, "nonexistent"))
	if !errors.Is(err, redis.ErrNil) {
		t.Fatalf("expected ErrNil, got %v", err)
	}
}

func TestIntegration_SetWithTTL(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()
	key := uniqueKey(t, "ttl")
	t.Cleanup(func() { _, _ = comp.Del(context.Background(), key) })

	if err := comp.Set(ctx, key, "expires", 500*time.Millisecond); err != nil {
		t.Fatalf("Set with TTL: %v", err)
	}

	// Should exist immediately.
	if _, err := comp.Get(ctx, key); err != nil {
		t.Fatalf("Get before expiry: %v", err)
	}

	time.Sleep(600 * time.Millisecond)

	// Should be gone after TTL.
	_, err := comp.Get(ctx, key)
	if !errors.Is(err, redis.ErrNil) {
		t.Fatalf("expected ErrNil after TTL expiry, got %v", err)
	}
}

func TestIntegration_Del(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()
	k1, k2 := uniqueKey(t, "del1"), uniqueKey(t, "del2")

	_ = comp.Set(ctx, k1, "a", 0)
	_ = comp.Set(ctx, k2, "b", 0)

	n, err := comp.Del(ctx, k1, k2)
	if err != nil {
		t.Fatalf("Del: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}

	// Both keys should be gone.
	if _, err := comp.Get(ctx, k1); !errors.Is(err, redis.ErrNil) {
		t.Fatalf("expected ErrNil for %q, got %v", k1, err)
	}
}

func TestIntegration_Exists(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()
	key := uniqueKey(t, "exists")
	t.Cleanup(func() { _, _ = comp.Del(context.Background(), key) })

	n, err := comp.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists (before set): %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}

	_ = comp.Set(ctx, key, "x", 0)
	n, err = comp.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists (after set): %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
}

func TestIntegration_Expire_TTL(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()
	key := uniqueKey(t, "expire")
	t.Cleanup(func() { _, _ = comp.Del(context.Background(), key) })

	_ = comp.Set(ctx, key, "v", 0)

	ok, err := comp.Expire(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if !ok {
		t.Fatal("Expire returned false for existing key")
	}

	ttl, err := comp.TTL(ctx, key)
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("unexpected TTL: %v", ttl)
	}
}

func TestIntegration_Scan(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	prefix := uniqueKey(t, "scan")
	keys := []string{prefix + ":a", prefix + ":b", prefix + ":c"}
	for _, k := range keys {
		_ = comp.Set(ctx, k, "v", 0)
	}
	t.Cleanup(func() { _, _ = comp.Del(context.Background(), keys...) })

	found, err := comp.Scan(ctx, prefix+":*")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(found), found)
	}
}

func TestIntegration_Scan_NoMatch(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)

	found, err := comp.Scan(context.Background(), "_sc_redis_test:no_match_ever:*")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(found))
	}
}

// TestIntegration_TLS_Start connects to a TLS-enabled Redis instance.
//
// Gated on the REDIS_TLS_ADDR env var (host:port). Skipped when unset, so
// CI without a TLS-capable redis remains green. Optional env:
//
//	REDIS_TLS_CA_FILE       path to CA bundle (else system trust)
//	REDIS_TLS_SERVER_NAME   SNI override (else host portion of REDIS_TLS_ADDR)
//	REDIS_TLS_USER          ACL username
//	REDIS_TLS_PASS          ACL password
//	REDIS_TLS_INSECURE      "1" to skip cert verification (dev only)
func TestIntegration_TLS_Start(t *testing.T) {
	addr := os.Getenv("REDIS_TLS_ADDR")
	if addr == "" {
		t.Skip("REDIS_TLS_ADDR not set; skipping TLS integration test")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("REDIS_TLS_ADDR %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("REDIS_TLS_ADDR port %q: %v", portStr, err)
	}

	cfg := redis.Config{
		Host:                  host,
		Port:                  port,
		User:                  os.Getenv("REDIS_TLS_USER"),
		Pass:                  os.Getenv("REDIS_TLS_PASS"),
		ConnectTimeout:        10 * time.Second,
		TLS:                   true,
		TLSCAFile:             os.Getenv("REDIS_TLS_CA_FILE"),
		TLSServerName:         os.Getenv("REDIS_TLS_SERVER_NAME"),
		TLSInsecureSkipVerify: os.Getenv("REDIS_TLS_INSECURE") == "1",
	}
	comp := redis.New(cfg, redis.WithLogger(&testLogger{t}))

	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { errCh <- comp.Start(ctx, func() { close(readyCh) }) }()

	select {
	case <-readyCh:
	case err := <-errCh:
		cancel()
		t.Fatalf("Start failed: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("Start timed out")
	}

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer healthCancel()
	if err := comp.Health(healthCtx); err != nil {
		t.Fatalf("Health failed over TLS: %v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := comp.Stop(stopCtx); err != nil {
		t.Errorf("Stop: %v", err)
	}
	cancel()
	<-errCh
}

// Compile-time check that testAddr is used (avoids "declared but not used").
var _ = testAddr
