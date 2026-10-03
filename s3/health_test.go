package s3_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/s3"
)

// probeServer is a fake S3 endpoint that answers every HeadBucket with a fixed
// status and records the buckets it was asked about.
type probeServer struct {
	*httptest.Server

	mu      sync.Mutex
	status  int
	buckets []string
}

// setStatus changes the status every later request is answered with.
func (ps *probeServer) setStatus(code int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.status = code
}

// firstBucket returns the bucket named by the first request, or "" if none.
func (ps *probeServer) firstBucket() string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.buckets) == 0 {
		return ""
	}
	return ps.buckets[0]
}

func newProbeServer(t *testing.T, status int) *probeServer {
	t.Helper()
	ps := &probeServer{status: status}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.buckets = append(ps.buckets, strings.Trim(r.URL.Path, "/"))
		status := ps.status
		ps.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(ps.Close)
	return ps
}

// startComponent starts comp against a live test server and waits for ready,
// returning the Start error (nil when the component became ready).
func startComponent(t *testing.T, comp *s3.Component) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = comp.Stop(context.Background())
	})

	ready := make(chan struct{})
	errCh := make(chan error, 1)
	go func() { errCh <- comp.Start(ctx, func() { close(ready) }) }()

	select {
	case <-ready:
		return nil
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Start neither became ready nor returned")
		return nil
	}
}

func probeConfig(endpoint string, healthBucket string) s3.Config {
	return s3.Config{
		Endpoint:         endpoint,
		Region:           "us-east-1",
		KeyID:            "key",
		Secret:           "secret",
		HealthBucket:     healthBucket,
		PathStyleForcing: true,
		ConnectTimeout:   5 * time.Second,
	}
}

// Without HealthBucket the probe stays lenient: a 403 answer proves the
// endpoint and signing chain work, which is all the synthetic bucket can tell.
func TestHealth_SyntheticProbe_ForbiddenIsHealthy(t *testing.T) {
	srv := newProbeServer(t, http.StatusForbidden)
	comp := s3.New(probeConfig(srv.URL, ""), s3.WithLogger(&testLogger{t}))

	if err := startComponent(t, comp); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := comp.Health(context.Background()); err != nil {
		t.Fatalf("Health = %v, want nil", err)
	}
	if got := srv.firstBucket(); got != "samsara-health-probe" {
		t.Errorf("probed bucket = %q, want the synthetic name", got)
	}
}

// With HealthBucket set, a 403 means the credential is not scoped for the
// bucket the application uses — that is a failure, not a healthy endpoint.
func TestHealth_ConfiguredBucket_ForbiddenIsUnhealthy(t *testing.T) {
	srv := newProbeServer(t, http.StatusOK)
	comp := s3.New(probeConfig(srv.URL, "media"), s3.WithLogger(&testLogger{t}))

	if err := startComponent(t, comp); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := srv.firstBucket(); got != "media" {
		t.Errorf("probed bucket = %q, want %q", got, "media")
	}

	srv.setStatus(http.StatusForbidden)
	err := comp.Health(context.Background())
	if !errors.Is(err, s3.ErrProbeForbidden) {
		t.Fatalf("Health = %v, want ErrProbeForbidden", err)
	}
}

// A 404 on the configured bucket is reported distinctly from a mis-scoped
// credential, so the operator knows which fix applies.
func TestHealth_ConfiguredBucket_MissingIsUnhealthy(t *testing.T) {
	srv := newProbeServer(t, http.StatusNotFound)
	comp := s3.New(probeConfig(srv.URL, "media"), s3.WithLogger(&testLogger{t}))

	err := startComponent(t, comp)
	if !errors.Is(err, s3.ErrProbeBucketMissing) {
		t.Fatalf("Start = %v, want ErrProbeBucketMissing", err)
	}
}

// The healthy path: a configured bucket the credential can see.
func TestHealth_ConfiguredBucket_OK(t *testing.T) {
	srv := newProbeServer(t, http.StatusOK)
	comp := s3.New(probeConfig(srv.URL, "media"), s3.WithLogger(&testLogger{t}))

	if err := startComponent(t, comp); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := comp.Health(context.Background()); err != nil {
		t.Fatalf("Health = %v, want nil", err)
	}
}
