package rabbitmq_test

import (
	"context"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/rabbitmq"
)

// ----------------------------------------------------------------------------
// Construction
// ----------------------------------------------------------------------------

func TestNew_DefaultName(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	if comp.Name() != "rabbitmq" {
		t.Fatalf("expected name %q, got %q", "rabbitmq", comp.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{}, rabbitmq.WithName("events-broker"))
	if comp.Name() != "events-broker" {
		t.Fatalf("expected %q, got %q", "events-broker", comp.Name())
	}
}

func TestNew_WithLogger(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{}, rabbitmq.WithLogger(&testLogger{t}))
	if comp == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// Lifecycle (no broker needed)
// ----------------------------------------------------------------------------

func TestStop_BeforeStart(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	done := make(chan error, 1)
	go func() { done <- comp.Stop(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked unexpectedly before Start")
	}
}

func TestStop_Idempotent(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	ctx := context.Background()
	for range 3 {
		if err := comp.Stop(ctx); err != nil {
			t.Fatalf("repeated Stop returned error: %v", err)
		}
	}
}

func TestStart_UnreachableHost(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{
		Host:           "192.0.2.1", // TEST-NET — guaranteed unreachable
		ConnectTimeout: 300 * time.Millisecond,
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(context.Background(), func() {
			t.Error("ready() must not be called when connection fails")
		})
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil error from Start with unreachable host")
		}
		t.Logf("Start correctly returned: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline")
	}
}

func TestStart_ContextCancelledBeforeConnect(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{
		Host:           "192.0.2.1",
		ConnectTimeout: 10 * time.Second, // long timeout
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	errCh := make(chan error, 1)
	go func() {
		errCh <- comp.Start(ctx, func() {
			t.Error("ready() must not be called")
		})
	}()
	select {
	case err := <-errCh:
		// nil is correct: clean shutdown means Start returns nil
		t.Logf("Start returned: %v (nil = clean shutdown)", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within deadline after context cancel")
	}
}

func TestHealth_BeforeStart(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("expected error from Health before Start")
	}
}

// ----------------------------------------------------------------------------
// DeclareExchange + Subscribe (no broker — only verifies no panic/error)
// ----------------------------------------------------------------------------

func TestDeclareExchange_BeforeStart(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	if err := comp.DeclareExchange("events", rabbitmq.ExchangeTopic, true); err != nil {
		t.Fatalf("DeclareExchange before Start returned error: %v", err)
	}
}

func TestSubscribe_BeforeStart(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	if err := comp.Subscribe("events", "user.created", nil); err != nil {
		t.Fatalf("Subscribe before Start returned error: %v", err)
	}
}

func TestSubscribeWithKey_BeforeStart(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	if err := comp.SubscribeWithKey("events", "user.queue", "user.#", nil); err != nil {
		t.Fatalf("SubscribeWithKey before Start returned error: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Publish (no broker — verifies error when channel is nil)
// ----------------------------------------------------------------------------

func TestPublish_BeforeStart_ReturnsError(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	err := comp.Publish(context.Background(), "events", "user.created",
		rabbitmq.ContentTypeJSON, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error from Publish when not connected")
	}
}

func TestPublishWithType_BeforeStart_ReturnsError(t *testing.T) {
	comp := rabbitmq.New(rabbitmq.Config{})
	err := comp.PublishWithType(context.Background(), "events", "user.created",
		rabbitmq.ContentTypeJSON, "UserCreated", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error from PublishWithType when not connected")
	}
}

// ----------------------------------------------------------------------------
// ContentType constants
// ----------------------------------------------------------------------------

func TestContentType_Values(t *testing.T) {
	cases := []struct {
		ct   rabbitmq.ContentType
		want string
	}{
		{rabbitmq.ContentTypeJSON, "application/json"},
		{rabbitmq.ContentTypeJSONUTF8, "application/json; charset=utf-8"},
		{rabbitmq.ContentTypeText, "text/plain"},
		{rabbitmq.ContentTypeBytes, "application/octet-stream"},
	}
	for _, tc := range cases {
		if string(tc.ct) != tc.want {
			t.Errorf("ContentType %v: got %q, want %q", tc.ct, tc.ct, tc.want)
		}
	}
}

// ----------------------------------------------------------------------------
// ExchangeKind constants
// ----------------------------------------------------------------------------

func TestExchangeKind_Values(t *testing.T) {
	cases := []struct {
		k    rabbitmq.ExchangeKind
		want string
	}{
		{rabbitmq.ExchangeDirect, "direct"},
		{rabbitmq.ExchangeTopic, "topic"},
		{rabbitmq.ExchangeFanout, "fanout"},
		{rabbitmq.ExchangeHeaders, "headers"},
	}
	for _, tc := range cases {
		if string(tc.k) != tc.want {
			t.Errorf("ExchangeKind %v: got %q, want %q", tc.k, tc.k, tc.want)
		}
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type testLogger struct{ t *testing.T }

func (l *testLogger) Info(msg string, args ...any)  { l.t.Log(append([]any{"INFO ", msg}, args...)...) }
func (l *testLogger) Warn(msg string, args ...any)  { l.t.Log(append([]any{"WARN ", msg}, args...)...) }
func (l *testLogger) Error(msg string, args ...any) { l.t.Log(append([]any{"ERROR", msg}, args...)...) }
func (l *testLogger) Debug(msg string, args ...any) { l.t.Log(append([]any{"DEBUG", msg}, args...)...) }
