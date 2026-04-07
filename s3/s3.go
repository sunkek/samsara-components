// Package s3 provides a [github.com/sunkek/samsara]-compatible S3 component
// backed by the AWS SDK v2.
//
// It works with any S3-compatible storage provider: AWS S3, Yandex Cloud
// Object Storage, SeaweedFS, Cloudflare R2, and others.
//
// # Usage
//
//	store := s3.New(s3.Config{
//	    Endpoint: "https://s3.us-east-1.amazonaws.com",
//	    Region:   "us-east-1",
//	    KeyID:    "AKIAIOSFODNN7EXAMPLE",
//	    Secret:   "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
//	})
//	sup.Add(store,
//	    samsara.WithTier(samsara.TierSignificant),
//	    samsara.WithRestartPolicy(samsara.AlwaysRestart(5*time.Second)),
//	)
//
// Domain adapters receive *Component and call Upload, Download,
// PresignUpload, PresignDownload, Delete, and ListKeys.
package s3

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config holds all connection parameters for the S3 component.
type Config struct {
	// Endpoint is the S3 endpoint URL.
	// Required for non-AWS providers (Yandex, MinIO, R2, etc.).
	// Leave empty to use the default AWS endpoint.
	Endpoint string

	// Region is the AWS region or equivalent.
	// Example: "us-east-1", "us-east-1".
	Region string

	// KeyID is the access key ID (AWS_ACCESS_KEY_ID equivalent).
	KeyID string

	// Secret is the secret access key (AWS_SECRET_ACCESS_KEY equivalent).
	Secret string

	// ConnectTimeout is the deadline for the initial connectivity check
	// during Start. Defaults to 10 s.
	ConnectTimeout time.Duration

	// PresignTTL is the default TTL for presigned URLs.
	// Individual calls can override this. Defaults to 15 minutes.
	PresignTTL time.Duration

	// PathStyleForcing enables path-style S3 addressing (bucket in URL path
	// instead of subdomain). Required by MinIO and some other providers.
	PathStyleForcing bool
}

func (c Config) connectTimeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return 10 * time.Second
}

func (c Config) presignTTL() time.Duration {
	if c.PresignTTL > 0 {
		return c.PresignTTL
	}
	return 15 * time.Minute
}

// Logger is satisfied by [log/slog.Logger] and most structured loggers.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// Component is a samsara-compatible S3 component.
// Obtain one with [New]; register it with a samsara supervisor.
type Component struct {
	cfg  Config
	log  Logger
	name string

	// mu guards client, presigner, and stopCh.
	mu        sync.RWMutex
	client    *s3.Client
	presigner *s3.PresignClient
	stopCh    chan struct{}
}

// New creates a Component from the supplied config.
// The S3 client is not initialised until [Component.Start] is called.
func New(cfg Config, opts ...Option) *Component {
	c := &Component{
		cfg:    cfg,
		log:    nopLogger{},
		name:   "s3",
		stopCh: make(chan struct{}), // initialised so Stop-before-Start is safe
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Option configures a [Component].
type Option func(*Component)

// WithLogger attaches a structured logger to the component.
// [log/slog.Logger] satisfies [Logger] directly.
func WithLogger(l Logger) Option {
	return func(c *Component) { c.log = l }
}

// WithName overrides the component name returned by [Component.Name].
// Useful when using multiple S3 providers with the same supervisor.
func WithName(name string) Option {
	return func(c *Component) { c.name = name }
}

// Compile-time assertion: *Component satisfies the samsara component and
// health-checker interfaces without importing the samsara package.
var (
	_ interface {
		Name() string
		Start(ctx context.Context, ready func()) error
		Stop(ctx context.Context) error
	} = (*Component)(nil)

	_ interface {
		Health(ctx context.Context) error
	} = (*Component)(nil)
)

// Name implements samsara.Component.
func (c *Component) Name() string { return c.name }

func (c *Component) getClient() *s3.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

func (c *Component) getPresigner() *s3.PresignClient {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.presigner
}

// Start loads the AWS config, initialises the S3 client, verifies connectivity,
// calls ready(), then blocks until Stop or ctx cancellation.
//
// Connectivity is verified using HeadBucket on a well-formed but likely
// nonexistent bucket — this exercises the signing chain and endpoint without
// requiring ListBuckets permission (which is often not granted to
// service-account credentials).
//
// Start is safe to call multiple times across restarts.
func (c *Component) Start(ctx context.Context, ready func()) error {
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	c.mu.Unlock()

	cp := credProvider{keyID: c.cfg.KeyID, secret: c.cfg.Secret}

	connectCtx, cancel := context.WithTimeout(ctx, c.cfg.connectTimeout())
	defer cancel()

	awsCfg, err := config.LoadDefaultConfig(
		connectCtx,
		config.WithRegion(c.cfg.Region),
		config.WithCredentialsProvider(cp),
	)
	if err != nil {
		return fmt.Errorf("s3: load config: %w", err)
	}

	clientOpts := []func(*s3.Options){
		func(o *s3.Options) {
			o.UsePathStyle = c.cfg.PathStyleForcing
		},
	}
	if c.cfg.Endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = &c.cfg.Endpoint
		})
	}

	client := s3.NewFromConfig(awsCfg, clientOpts...)
	presigner := s3.NewPresignClient(client)

	// Verify the signing chain and endpoint are functional. HeadBucket with
	// a synthetic bucket name will return NoSuchBucket (404) or
	// AccessDenied — both confirm the endpoint is reachable and credentials
	// are being signed correctly. Only a network-level failure returns an
	// error we treat as a startup failure.
	if err := verifyConnectivity(connectCtx, client); err != nil {
		return fmt.Errorf("s3: connectivity check failed: %w", err)
	}

	c.mu.Lock()
	c.client = client
	c.presigner = presigner
	c.mu.Unlock()

	c.log.Info("s3: connected", "endpoint", c.cfg.Endpoint, "region", c.cfg.Region)
	ready()

	select {
	case <-stopCh:
	case <-ctx.Done():
	}
	return nil
}

// Stop signals Start to return. The S3 client is stateless and holds no
// persistent connections, so there is nothing to close.
func (c *Component) Stop(_ context.Context) error {
	c.mu.Lock()
	ch := c.stopCh
	closed := make(chan struct{})
	close(closed)
	c.stopCh = closed
	c.mu.Unlock()

	select {
	case <-ch:
	default:
		close(ch)
	}
	return nil
}

// Health implements samsara.HealthChecker.
// Returns nil if the endpoint is reachable and credentials are valid.
func (c *Component) Health(ctx context.Context) error {
	client := c.getClient()
	if client == nil {
		return fmt.Errorf("s3: client not initialised")
	}
	return verifyConnectivity(ctx, client)
}

// verifyConnectivity sends a HeadBucket request with a synthetic bucket name.
// Any response (including 404/403) confirms the endpoint is reachable; only
// a network error or credential-signing failure is treated as a failure.
func verifyConnectivity(ctx context.Context, client *s3.Client) error {
	// "_samsara-health-check" is intentionally not a real bucket name.
	// The response will be 404 NoSuchBucket or 403 AccessDenied — both prove
	// the endpoint and signing chain are working.
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: ptrOf("_samsara-health-check"),
	})
	if err == nil {
		return nil // unexpectedly found; still healthy
	}
	// 404 and 403 responses from the server confirm connectivity.
	if isExpectedHealthError(err) {
		return nil
	}
	return err
}
