package s3_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/s3"
)

// observation is one call recorded by the test sink.
type observation struct {
	op  string
	d   time.Duration
	err error
}

// recorder is a Config.OnOperation sink that captures every call.
type recorder struct {
	mu   sync.Mutex
	obs  []observation
	hook func()
}

func (r *recorder) record(op string, d time.Duration, err error) {
	if r.hook != nil {
		r.hook()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.obs = append(r.obs, observation{op, d, err})
}

func (r *recorder) all() []observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observation(nil), r.obs...)
}

func TestOnOperation_ReportsEveryStorageOperation(t *testing.T) {
	ctx := context.Background()
	r := &recorder{}
	c := s3.New(s3.Config{OnOperation: r.record})

	// Never started, so every operation takes the not-initialised path —
	// enough to prove each one reports, without a live endpoint.
	_ = c.Upload(ctx, s3.UploadRequest{Bucket: "b", Key: "k", Body: strings.NewReader("x")})
	_, _ = c.Download(ctx, "b", "k")
	_ = c.Delete(ctx, "b", "k")
	_, _ = c.DeleteByPrefix(ctx, "b", "p")
	_, _ = c.ListKeys(ctx, "b", "p")
	_, _ = c.PresignDownload(ctx, s3.PresignRequest{Bucket: "b", Key: "k"})
	_, _ = c.PresignUpload(ctx, s3.PresignRequest{Bucket: "b", Key: "k"})

	want := []string{
		"s3.upload", "s3.download", "s3.delete", "s3.delete_by_prefix",
		"s3.list_keys", "s3.presign_download", "s3.presign_upload",
	}
	got := r.all()
	if len(got) != len(want) {
		t.Fatalf("got %d observations, want %d: %+v", len(got), len(want), got)
	}
	for i, op := range want {
		if got[i].op != op {
			t.Errorf("observation %d: op = %q, want %q", i, got[i].op, op)
		}
		if got[i].err == nil {
			t.Errorf("observation %d (%s): reported nil error, want the not-initialised failure", i, op)
		}
	}
}

// DeleteByPrefix lists before it deletes. It must still report once — the
// caller made one call, and a nested s3.list_keys would double-count.
func TestOnOperation_DeleteByPrefixReportsOnce(t *testing.T) {
	r := &recorder{}
	c := s3.New(s3.Config{OnOperation: r.record})

	_, _ = c.DeleteByPrefix(context.Background(), "b", "p")

	got := r.all()
	if len(got) != 1 {
		t.Fatalf("got %d observations, want exactly 1: %+v", len(got), got)
	}
	if got[0].op != "s3.delete_by_prefix" {
		t.Errorf("op = %q, want %q", got[0].op, "s3.delete_by_prefix")
	}
}

func TestOnOperation_NilSinkIsNoOp(t *testing.T) {
	c := s3.New(s3.Config{})
	if err := c.Delete(context.Background(), "b", "k"); err == nil {
		t.Fatal("Delete error = nil, want the not-initialised failure")
	}
}

func TestOnOperation_PanickingSinkDoesNotReachCaller(t *testing.T) {
	r := &recorder{hook: func() { panic("sink exploded") }}
	c := s3.New(s3.Config{OnOperation: r.record})

	// The operation's own error must survive the sink's panic unchanged.
	if err := c.Delete(context.Background(), "b", "k"); err == nil {
		t.Fatal("Delete error = nil, want the not-initialised failure despite panicking sink")
	}
}

func TestConfig_ZeroValueLeavesMetricsDisabled(t *testing.T) {
	if (s3.Config{}).OnOperation != nil {
		t.Error("zero-value Config should not report metrics")
	}
}
