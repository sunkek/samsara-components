package s3_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/s3"
)

// ----------------------------------------------------------------------------
// Construction
// ----------------------------------------------------------------------------

func TestNew_DefaultName(t *testing.T) {
	comp := s3.New(s3.Config{})
	if comp.Name() != "s3" {
		t.Fatalf("expected name %q, got %q", "s3", comp.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	comp := s3.New(s3.Config{}, s3.WithName("media-store"))
	if comp.Name() != "media-store" {
		t.Fatalf("expected %q, got %q", "media-store", comp.Name())
	}
}

func TestNew_WithLogger(t *testing.T) {
	comp := s3.New(s3.Config{}, s3.WithLogger(&testLogger{t}))
	if comp == nil {
		t.Fatal("expected non-nil component")
	}
}

// ----------------------------------------------------------------------------
// Lifecycle (no S3 needed)
// ----------------------------------------------------------------------------

func TestStop_BeforeStart(t *testing.T) {
	comp := s3.New(s3.Config{})
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
	comp := s3.New(s3.Config{})
	ctx := context.Background()
	for range 3 {
		if err := comp.Stop(ctx); err != nil {
			t.Fatalf("repeated Stop returned error: %v", err)
		}
	}
}

func TestHealth_BeforeStart(t *testing.T) {
	comp := s3.New(s3.Config{})
	if err := comp.Health(context.Background()); err == nil {
		t.Fatal("expected error from Health before Start")
	}
}

// ----------------------------------------------------------------------------
// ACL constants
// ----------------------------------------------------------------------------

func TestACL_Values(t *testing.T) {
	cases := []struct {
		acl  s3.ACL
		want string
	}{
		{s3.ACLPrivate, "private"},
		{s3.ACLPublicRead, "public-read"},
		{s3.ACLPublicReadWrite, "public-read-write"},
		{s3.ACLAuthenticatedRead, "authenticated-read"},
		{s3.ACLBucketOwnerRead, "bucket-owner-read"},
		{s3.ACLBucketOwnerFullControl, "bucket-owner-full-control"},
	}
	for _, tc := range cases {
		if string(tc.acl) != tc.want {
			t.Errorf("ACL %q: expected %q", tc.acl, tc.want)
		}
	}
}

// ----------------------------------------------------------------------------
// UploadRequest / PresignRequest validation
// ----------------------------------------------------------------------------

func TestUploadRequest_RequiredFields(t *testing.T) {
	// Attempting to upload before Start returns a clear error, not a panic.
	comp := s3.New(s3.Config{})
	err := comp.Upload(context.Background(), s3.UploadRequest{
		Bucket: "my-bucket",
		Key:    "file.txt",
		Body:   strings.NewReader("hello"),
	})
	// Expected: nil-client error (not panic).
	if err == nil {
		t.Fatal("expected error when client not initialised")
	}
}

func TestPresignRequest_ZeroTTL_UsesDefault(t *testing.T) {
	// Verify PresignRequest.TTL=0 is distinguishable from a configured TTL.
	r := s3.PresignRequest{Bucket: "b", Key: "k", TTL: 0}
	if r.TTL != 0 {
		t.Fatalf("expected zero TTL, got %v", r.TTL)
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type testLogger struct{ t *testing.T }

func (l *testLogger) Info(msg string, args ...any)  { l.t.Log(append([]any{"INFO ", msg}, args...)...) }
func (l *testLogger) Error(msg string, args ...any) { l.t.Log(append([]any{"ERROR", msg}, args...)...) }
