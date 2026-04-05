//go:build integration

package rabbitmq_test

// Integration tests require a running RabbitMQ instance.
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
	"encoding/json"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sunkek/samsara-components/rabbitmq"
)

// testURI matches the docker-compose.yml credentials.
const testURI = "amqp://test:test@localhost:5672/test"

func testComp(t *testing.T) *rabbitmq.Component {
	t.Helper()
	return rabbitmq.New(
		rabbitmq.Config{URI: testURI, ConnectTimeout: 10 * time.Second},
		rabbitmq.WithLogger(&testLogger{t}),
	)
}

// startComp starts the component, waits for ready, and registers cleanup.
func startComp(t *testing.T, comp *rabbitmq.Component) {
	t.Helper()
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		errCh <- comp.Start(ctx, func() { close(readyCh) })
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

func TestIntegration_StartStop(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
}

func TestIntegration_Health(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)

	if err := comp.Health(context.Background()); err != nil {
		t.Fatalf("Health returned error on live connection: %v", err)
	}
}

func TestIntegration_Restart(t *testing.T) {
	comp := testComp(t)

	for i := range 2 {
		t.Logf("run %d", i+1)
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

func TestIntegration_DeclareExchangeAndPublish(t *testing.T) {
	comp := testComp(t)

	if err := comp.DeclareExchange("test.events", rabbitmq.ExchangeTopic, false); err != nil {
		t.Fatalf("DeclareExchange: %v", err)
	}

	startComp(t, comp)

	ctx := context.Background()
	err := comp.Publish(ctx, "test.events", "test.key",
		rabbitmq.ContentTypeJSON, []byte(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestIntegration_SubscribeAndReceive(t *testing.T) {
	type payload struct {
		Value string `json:"value"`
	}

	comp := testComp(t)

	if err := comp.DeclareExchange("test.sub", rabbitmq.ExchangeDirect, false); err != nil {
		t.Fatalf("DeclareExchange: %v", err)
	}

	received := make(chan payload, 1)
	err := comp.Subscribe("test.sub", "test.sub.queue", func(d amqp.Delivery) error {
		var p payload
		if err := json.Unmarshal(d.Body, &p); err != nil {
			return err
		}
		received <- p
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	startComp(t, comp)

	ctx := context.Background()
	want := payload{Value: "hello"}
	body, _ := json.Marshal(want)
	if err := comp.Publish(ctx, "test.sub", "test.sub.queue",
		rabbitmq.ContentTypeJSON, body); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-received:
		if got.Value != want.Value {
			t.Fatalf("expected value %q, got %q", want.Value, got.Value)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestIntegration_SubscribeWithKey_TopicRouting(t *testing.T) {
	comp := testComp(t)

	if err := comp.DeclareExchange("test.topic", rabbitmq.ExchangeTopic, false); err != nil {
		t.Fatalf("DeclareExchange: %v", err)
	}

	// Buffered for exactly 2: user.created and user.updated match "user.#";
	// order.placed does not and must not arrive.
	received := make(chan string, 2)

	err := comp.SubscribeWithKey("test.topic", "test.topic.queue", "user.#",
		func(d amqp.Delivery) error {
			received <- string(d.Body)
			return nil
		})
	if err != nil {
		t.Fatalf("SubscribeWithKey: %v", err)
	}

	startComp(t, comp)

	ctx := context.Background()
	for _, key := range []string{"user.created", "user.updated", "order.placed"} {
		if err := comp.Publish(ctx, "test.topic", key, rabbitmq.ContentTypeText, []byte(key)); err != nil {
			t.Fatalf("Publish %q: %v", key, err)
		}
	}

	// Collect the two expected messages with a generous deadline.
	var msgs []string
	deadline := time.After(10 * time.Second)
	for len(msgs) < 2 {
		select {
		case m := <-received:
			msgs = append(msgs, m)
		case <-deadline:
			t.Fatalf("timed out waiting for messages via user.# routing; got %d: %v", len(msgs), msgs)
		}
	}

	// Give the broker a moment to confirm no third message arrives.
	select {
	case extra := <-received:
		t.Fatalf("received unexpected message %q — order.placed must not match user.#", extra)
	case <-time.After(200 * time.Millisecond):
		// correct: nothing extra arrived
	}
}

func TestIntegration_HandlerError_Nacks(t *testing.T) {
	comp := testComp(t)

	if err := comp.DeclareExchange("test.nack", rabbitmq.ExchangeDirect, false); err != nil {
		t.Fatalf("DeclareExchange: %v", err)
	}

	attempts := 0
	received := make(chan struct{})
	err := comp.Subscribe("test.nack", "test.nack.queue", func(d amqp.Delivery) error {
		attempts++
		if attempts == 1 {
			return errors.New("first attempt fails")
		}
		close(received) // second attempt succeeds
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	startComp(t, comp)

	ctx := context.Background()
	if err := comp.Publish(ctx, "test.nack", "test.nack.queue",
		rabbitmq.ContentTypeJSON, []byte(`{}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-received:
		if attempts < 2 {
			t.Fatalf("expected at least 2 attempts (nack+retry), got %d", attempts)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for retry; attempts so far: %d", attempts)
	}
}

func TestIntegration_PublishWithType(t *testing.T) {
	comp := testComp(t)

	if err := comp.DeclareExchange("test.typed", rabbitmq.ExchangeDirect, false); err != nil {
		t.Fatalf("DeclareExchange: %v", err)
	}

	msgType := make(chan string, 1)
	err := comp.Subscribe("test.typed", "test.typed.queue", func(d amqp.Delivery) error {
		msgType <- d.Type
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	startComp(t, comp)

	ctx := context.Background()
	if err := comp.PublishWithType(ctx, "test.typed", "test.typed.queue",
		rabbitmq.ContentTypeJSON, "UserCreated", []byte(`{}`)); err != nil {
		t.Fatalf("PublishWithType: %v", err)
	}

	select {
	case got := <-msgType:
		if got != "UserCreated" {
			t.Fatalf("expected message type %q, got %q", "UserCreated", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for typed message")
	}
}
