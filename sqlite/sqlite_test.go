package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/sqlite"
)

// startComponent starts c in a goroutine and returns a function that stops it
// and asserts Start returned nil. It fails the test if ready() is not called
// within the timeout.
func startComponent(t *testing.T, c *sqlite.Component) (stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	errCh := make(chan error, 1)

	go func() { errCh <- c.Start(ctx, func() { close(ready) }) }()

	select {
	case <-ready:
	case err := <-errCh:
		cancel()
		t.Fatalf("Start returned before ready: %v", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("timed out waiting for ready()")
	}

	return func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := c.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("Start: %v", err)
		}
	}
}

// ----------------------------------------------------------------------------
// Driver escape hatch (ADR-0005)
// ----------------------------------------------------------------------------

func TestSQLDB_NilBeforeStart(t *testing.T) {
	comp := sqlite.New(sqlite.Config{})
	if comp.SQLDB() != nil {
		t.Fatal("expected SQLDB to be nil before Start")
	}
}

func TestSQLDB_NilAfterStop(t *testing.T) {
	comp := sqlite.New(sqlite.Config{})
	if err := comp.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if comp.SQLDB() != nil {
		t.Fatal("expected SQLDB to be nil after Stop")
	}
}

func TestSQLDB_NonNilWhileRunning(t *testing.T) {
	c := sqlite.New(sqlite.Config{})
	stop := startComponent(t, c)
	defer stop()

	if c.SQLDB() == nil {
		t.Fatal("expected SQLDB to be non-nil while running")
	}
}
