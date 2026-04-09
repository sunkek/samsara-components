package grpc

import (
	"fmt"
	"time"

	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// Config holds all configuration for the gRPC server component.
type Config struct {
	// Host is the listen address. Defaults to "0.0.0.0".
	Host string
	// Port is the listen port. Defaults to 9090.
	Port int

	// EnableReflection registers the gRPC server reflection service, which
	// lets tools like grpcurl introspect the server's API without the .proto
	// files. Defaults to false — enable only in development/staging.
	EnableReflection bool

	// MaxRecvMsgSizeMB is the maximum message size the server will receive
	// in megabytes. Defaults to 4 MB.
	MaxRecvMsgSizeMB int
	// MaxSendMsgSizeMB is the maximum message size the server will send
	// in megabytes. Defaults to 4 MB.
	MaxSendMsgSizeMB int

	// KeepaliveTime is how long the server waits for activity before sending
	// a keepalive ping to the client. Defaults to 2 minutes.
	KeepaliveTime time.Duration
	// KeepaliveTimeout is how long the server waits for a ping ack before
	// closing the connection. Defaults to 20 seconds.
	KeepaliveTimeout time.Duration

	// MaxConnectionIdle is how long an idle client connection is kept open.
	// Defaults to 5 minutes.
	MaxConnectionIdle time.Duration
	// MaxConnectionAge is the maximum duration a connection may exist before
	// it is gracefully closed. Defaults to unlimited (0 = disabled).
	MaxConnectionAge time.Duration
}

func (c Config) addr() string {
	host := c.Host
	if host == "" {
		host = "0.0.0.0"
	}
	port := c.Port
	if port == 0 {
		port = 9090
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func (c Config) maxRecvMsgSizeBytes() int {
	if c.MaxRecvMsgSizeMB > 0 {
		return c.MaxRecvMsgSizeMB * 1024 * 1024
	}
	return 4 * 1024 * 1024
}

func (c Config) maxSendMsgSizeBytes() int {
	if c.MaxSendMsgSizeMB > 0 {
		return c.MaxSendMsgSizeMB * 1024 * 1024
	}
	return 4 * 1024 * 1024
}

func (c Config) keepaliveTime() time.Duration {
	if c.KeepaliveTime > 0 {
		return c.KeepaliveTime
	}
	return 2 * time.Minute
}

func (c Config) keepaliveTimeout() time.Duration {
	if c.KeepaliveTimeout > 0 {
		return c.KeepaliveTimeout
	}
	return 20 * time.Second
}

func (c Config) maxConnectionIdle() time.Duration {
	if c.MaxConnectionIdle > 0 {
		return c.MaxConnectionIdle
	}
	return 5 * time.Minute
}

// keepaliveOptions builds the grpc.ServerOptions for message size limits and
// keepalive policy. Called once per Start to ensure clean per-run config.
//
// All keepalive parameters are combined into a single KeepaliveParams call.
// Passing two separate KeepaliveParams options causes the second struct to
// overwrite the first: zero-valued fields in the second silently clear the
// non-zero values set by the first (e.g. MaxConnectionAge > 0 would zero out
// Time, Timeout, and MaxConnectionIdle).
func (c Config) keepaliveOptions() []grpclib.ServerOption {
	kp := keepalive.ServerParameters{
		Time:              c.keepaliveTime(),
		Timeout:           c.keepaliveTimeout(),
		MaxConnectionIdle: c.maxConnectionIdle(),
	}
	if c.MaxConnectionAge > 0 {
		kp.MaxConnectionAge = c.MaxConnectionAge
		kp.MaxConnectionAgeGrace = 5 * time.Second
	}
	return []grpclib.ServerOption{
		grpclib.MaxRecvMsgSize(c.maxRecvMsgSizeBytes()),
		grpclib.MaxSendMsgSize(c.maxSendMsgSizeBytes()),
		grpclib.KeepaliveParams(kp),
		// Enforce client keepalive policy to prevent poorly-behaved clients
		// from sending pings too aggressively and starving real traffic.
		grpclib.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	}
}
