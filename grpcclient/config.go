package grpcclient

import (
	"time"

	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// Config holds all configuration for the gRPC client component.
type Config struct {
	// Target is the gRPC server address. Accepts any target string supported
	// by gRPC's name resolver: plain "host:port", "dns:///host:port", or a
	// custom scheme. Required — no default.
	Target string

	// ConnectTimeout is the deadline for establishing a READY connection
	// during Start. Defaults to 10 s.
	ConnectTimeout time.Duration

	// MaxRecvMsgSizeMB is the maximum message size the client will receive
	// in megabytes. Defaults to 4 MB.
	MaxRecvMsgSizeMB int
	// MaxSendMsgSizeMB is the maximum message size the client will send
	// in megabytes. Defaults to 4 MB.
	MaxSendMsgSizeMB int

	// KeepaliveTime is how long the client waits for activity before sending
	// a keepalive ping to the server. Defaults to 30 seconds.
	KeepaliveTime time.Duration
	// KeepaliveTimeout is how long the client waits for a ping ack before
	// closing the connection. Defaults to 10 seconds.
	KeepaliveTimeout time.Duration
	// KeepalivePermitWithoutStream allows keepalive pings even when there
	// are no active RPCs. Defaults to true.
	KeepalivePermitWithoutStream *bool

	// TLS enables TLS transport credentials. When false (default) the client
	// dials with insecure (plaintext) credentials and all TLS* fields are
	// ignored.
	TLS bool
	// TLSServerName overrides the server name used for certificate
	// verification. Defaults to the hostname derived from Target.
	TLSServerName string
	// TLSCAFile is a CA bundle (PEM) used to verify the server certificate.
	// When empty, the system cert pool is used.
	TLSCAFile string
	// TLSCertFile / TLSKeyFile are a client certificate and key (PEM) for
	// mTLS. Both must be set or both empty.
	TLSCertFile string
	TLSKeyFile  string
	// TLSMinVersion is the minimum TLS version: "1.2" (default) or "1.3".
	TLSMinVersion string
	// TLSInsecureSkipVerify disables server certificate verification.
	// Never enable in production.
	TLSInsecureSkipVerify bool
}

func (c Config) connectTimeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return 10 * time.Second
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
	return 30 * time.Second
}

func (c Config) keepaliveTimeout() time.Duration {
	if c.KeepaliveTimeout > 0 {
		return c.KeepaliveTimeout
	}
	return 10 * time.Second
}

func (c Config) keepalivePermitWithoutStream() bool {
	if c.KeepalivePermitWithoutStream != nil {
		return *c.KeepalivePermitWithoutStream
	}
	return true
}

// dialOptions builds the grpc.DialOptions for message size limits and
// keepalive policy. Called once per Start to ensure clean per-run config.
func (c Config) dialOptions() []grpclib.DialOption {
	return []grpclib.DialOption{
		grpclib.WithDefaultCallOptions(
			grpclib.MaxCallRecvMsgSize(c.maxRecvMsgSizeBytes()),
			grpclib.MaxCallSendMsgSize(c.maxSendMsgSizeBytes()),
		),
		grpclib.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                c.keepaliveTime(),
			Timeout:             c.keepaliveTimeout(),
			PermitWithoutStream: c.keepalivePermitWithoutStream(),
		}),
	}
}
